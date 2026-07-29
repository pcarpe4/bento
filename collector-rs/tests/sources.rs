//! Integration tests for the HTTP-based sources, using a minimal in-process
//! HTTP server so no external services or dev-dependencies are needed.
//!
//! These drive the compiled binary end-to-end: config file -> collect once
//! (interval 0s) -> stdout destination -> parse the emitted JSON lines.

use std::io::Write;
use std::process::Command;

use tokio::io::{AsyncReadExt, AsyncWriteExt};
use tokio::net::TcpListener;

/// Serves canned JSON responses per path over raw HTTP/1.1 until dropped.
async fn spawn_server(routes: Vec<(&'static str, String)>) -> String {
    let listener = TcpListener::bind("127.0.0.1:0").await.unwrap();
    let addr = listener.local_addr().unwrap();
    tokio::spawn(async move {
        loop {
            let Ok((mut stream, _)) = listener.accept().await else {
                return;
            };
            let routes = routes.clone();
            tokio::spawn(async move {
                let mut buf = vec![0u8; 8192];
                let mut read = 0;
                // Read until end of request headers.
                while !buf[..read].windows(4).any(|w| w == b"\r\n\r\n") {
                    match stream.read(&mut buf[read..]).await {
                        Ok(0) | Err(_) => return,
                        Ok(n) => read += n,
                    }
                }
                let request = String::from_utf8_lossy(&buf[..read]).to_string();
                let path = request.split_whitespace().nth(1).unwrap_or("/").to_string();

                let response = match routes.iter().find(|(p, _)| *p == path) {
                    Some((_, body)) => format!(
                        "HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: {}\r\nConnection: close\r\n\r\n{}",
                        body.len(),
                        body
                    ),
                    None => "HTTP/1.1 404 Not Found\r\nContent-Length: 0\r\nConnection: close\r\n\r\n".to_string(),
                };
                let _ = stream.write_all(response.as_bytes()).await;
            });
        }
    });
    format!("http://{addr}")
}

/// Runs the collector binary against a config, returning emitted JSON lines.
fn run_collector(config: &str) -> Vec<serde_json::Value> {
    let mut config_file = tempfile();
    config_file.write_all(config.as_bytes()).unwrap();
    let path = config_file.path.clone();

    let output = Command::new(env!("CARGO_BIN_EXE_collector"))
        .args(["-config", &path])
        .output()
        .expect("failed to run collector binary");
    assert!(
        output.status.success(),
        "collector failed: {}",
        String::from_utf8_lossy(&output.stderr)
    );
    String::from_utf8(output.stdout)
        .unwrap()
        .lines()
        .map(|line| serde_json::from_str(line).expect("stdout line is not JSON"))
        .collect()
}

struct TempFile {
    path: String,
    file: std::fs::File,
}

impl Write for TempFile {
    fn write(&mut self, buf: &[u8]) -> std::io::Result<usize> {
        self.file.write(buf)
    }
    fn flush(&mut self) -> std::io::Result<()> {
        self.file.flush()
    }
}

impl Drop for TempFile {
    fn drop(&mut self) {
        let _ = std::fs::remove_file(&self.path);
    }
}

fn tempfile() -> TempFile {
    static COUNTER: std::sync::atomic::AtomicU32 = std::sync::atomic::AtomicU32::new(0);
    let unique = COUNTER.fetch_add(1, std::sync::atomic::Ordering::Relaxed);
    let path = std::env::temp_dir()
        .join(format!(
            "collector-test-{}-{unique}.yaml",
            std::process::id()
        ))
        .to_string_lossy()
        .to_string();
    let file = std::fs::File::create(&path).unwrap();
    TempFile { path, file }
}

#[tokio::test(flavor = "multi_thread")]
async fn http_api_source_collects_and_splits_arrays() {
    let base = spawn_server(vec![(
        "/v1/assets",
        r#"[{"id":"a"},{"id":"b"}]"#.to_string(),
    )])
    .await;

    let lines = tokio::task::spawn_blocking(move || {
        run_collector(&format!(
            r#"
sources:
  - name: api
    type: http_api
    interval: 0s
    config:
      urls: [{base}/v1/assets]
destinations:
  - name: out
    type: stdout
"#
        ))
    })
    .await
    .unwrap();

    assert_eq!(lines.len(), 2);
    assert_eq!(lines[0]["data"]["id"], "a");
    assert_eq!(lines[0]["meta"]["source"], "api");
    assert_eq!(lines[0]["meta"]["source_type"], "http_api");
    assert_eq!(lines[1]["data"]["id"], "b");
}

#[tokio::test(flavor = "multi_thread")]
async fn redfish_source_expands_collections() {
    let base = spawn_server(vec![
        (
            "/redfish/v1/Systems",
            r#"{"Members":[{"@odata.id":"/redfish/v1/Systems/S1"}]}"#.to_string(),
        ),
        (
            "/redfish/v1/Systems/S1",
            r#"{"@odata.id":"/redfish/v1/Systems/S1","Name":"System","Model":"PowerEdge R750"}"#
                .to_string(),
        ),
    ])
    .await;

    let lines = tokio::task::spawn_blocking(move || {
        run_collector(&format!(
            r#"
sources:
  - name: idrac
    type: redfish
    interval: 0s
    config:
      urls: [{base}]
      username: svc
      password: secret
destinations:
  - name: out
    type: stdout
"#
        ))
    })
    .await
    .unwrap();

    assert_eq!(lines.len(), 1);
    assert_eq!(lines[0]["data"]["Model"], "PowerEdge R750");
    assert_eq!(lines[0]["meta"]["odata_id"], "/redfish/v1/Systems/S1");
    assert_eq!(lines[0]["meta"]["name"], "System");
    assert_eq!(lines[0]["meta"]["source"], "idrac");
}
