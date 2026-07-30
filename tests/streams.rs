//! End-to-end tests driving the compiled binary: stream config -> poll once
//! (interval 0s) -> processors -> stdout, parsing the emitted JSON lines.
//! A minimal in-process HTTP server stands in for external systems.

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

fn write_temp_config(config: &str) -> (String, std::fs::File) {
    static COUNTER: std::sync::atomic::AtomicU32 = std::sync::atomic::AtomicU32::new(0);
    let unique = COUNTER.fetch_add(1, std::sync::atomic::Ordering::Relaxed);
    let path = std::env::temp_dir()
        .join(format!(
            "bento-rs-test-{}-{unique}.yaml",
            std::process::id()
        ))
        .to_string_lossy()
        .to_string();
    let mut file = std::fs::File::create(&path).unwrap();
    file.write_all(config.as_bytes()).unwrap();
    (path, file)
}

/// Runs `bento-rs run <config>` and returns emitted stdout JSON lines.
fn run_stream(config: &str) -> Vec<serde_json::Value> {
    let (path, _file) = write_temp_config(config);
    let output = Command::new(env!("CARGO_BIN_EXE_bento-rs"))
        .args(["run", &path])
        .output()
        .expect("failed to run bento-rs binary");
    let _ = std::fs::remove_file(&path);
    assert!(
        output.status.success(),
        "bento-rs failed: {}",
        String::from_utf8_lossy(&output.stderr)
    );
    String::from_utf8(output.stdout)
        .unwrap()
        .lines()
        .map(|line| serde_json::from_str(line).expect("stdout line is not JSON"))
        .collect()
}

#[tokio::test(flavor = "multi_thread")]
async fn generate_through_processors_to_stdout() {
    let lines = tokio::task::spawn_blocking(|| {
        run_stream(
            r#"
input:
  generate:
    json: {kind: vm, status: {phase: Running}, secret: hunter2}
    count: 2
    interval: 0s
pipeline:
  processors:
    - filter:
        path: status.phase
        equals: Running
    - set:
        path: origin
        value: "${meta:stream}"
    - remove:
        paths: [secret]
    - set_meta:
        key: kind
        value: "${data:kind}"
output:
  stdout: {}
"#,
        )
    })
    .await
    .unwrap();

    assert_eq!(lines.len(), 2);
    let msg = &lines[0];
    assert_eq!(msg["data"]["kind"], "vm");
    assert_eq!(msg["data"]["origin"], msg["meta"]["stream"]);
    assert!(msg["data"].get("secret").is_none(), "secret was removed");
    assert_eq!(msg["meta"]["kind"], "vm");
    assert_eq!(msg["meta"]["input_type"], "generate");
}

#[tokio::test(flavor = "multi_thread")]
async fn filter_drops_non_matching_messages() {
    let lines = tokio::task::spawn_blocking(|| {
        run_stream(
            r#"
input:
  generate:
    json: {status: {phase: Stopped}}
    interval: 0s
pipeline:
  processors:
    - filter:
        path: status.phase
        equals: Running
output:
  stdout: {}
"#,
        )
    })
    .await
    .unwrap();
    assert!(lines.is_empty());
}

#[tokio::test(flavor = "multi_thread")]
async fn http_client_input_splits_arrays() {
    let base = spawn_server(vec![(
        "/v1/assets",
        r#"[{"id":"a"},{"id":"b"}]"#.to_string(),
    )])
    .await;

    let lines = tokio::task::spawn_blocking(move || {
        run_stream(&format!(
            r#"
input:
  http_client:
    urls: [{base}/v1/assets]
    interval: 0s
output:
  stdout: {{}}
"#
        ))
    })
    .await
    .unwrap();

    assert_eq!(lines.len(), 2);
    assert_eq!(lines[0]["data"]["id"], "a");
    assert_eq!(lines[0]["meta"]["input_type"], "http_client");
}

#[tokio::test(flavor = "multi_thread")]
async fn redfish_input_expands_collections() {
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
        run_stream(&format!(
            r#"
input:
  redfish:
    urls: [{base}]
    username: svc
    password: secret
    interval: 0s
pipeline:
  processors:
    - select:
        paths: [Model, Name]
output:
  stdout: {{}}
"#
        ))
    })
    .await
    .unwrap();

    assert_eq!(lines.len(), 1);
    assert_eq!(lines[0]["data"]["Model"], "PowerEdge R750");
    assert!(
        lines[0]["data"].get("@odata.id").is_none(),
        "projected away"
    );
    assert_eq!(lines[0]["meta"]["odata_id"], "/redfish/v1/Systems/S1");
}

#[test]
fn lint_accepts_valid_and_rejects_unknown_component() {
    let (good, _f1) =
        write_temp_config("input:\n  generate:\n    json: {}\noutput:\n  stdout: {}\n");
    let ok = Command::new(env!("CARGO_BIN_EXE_bento-rs"))
        .args(["lint", &good])
        .output()
        .unwrap();
    let _ = std::fs::remove_file(&good);
    assert!(ok.status.success());

    let (bad, _f2) = write_temp_config("input:\n  kafka:\n    topic: x\noutput:\n  stdout: {}\n");
    let fail = Command::new(env!("CARGO_BIN_EXE_bento-rs"))
        .args(["lint", &bad])
        .output()
        .unwrap();
    let _ = std::fs::remove_file(&bad);
    assert!(!fail.status.success());
    assert!(String::from_utf8_lossy(&fail.stderr).contains("unknown input"));
}
