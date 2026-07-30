use std::time::Duration;

use super::config::{expand_env, interval_from, parse_duration, parse_stream};

#[test]
fn parses_stream_config() {
    let conf = parse_stream(
        r#"
input:
  http_client:
    urls: [https://example.com]
    interval: 1m
pipeline:
  processors:
    - filter:
        path: status
        equals: active
    - set:
        path: collected
        value: true
output:
  stdout: {}
"#,
    )
    .unwrap();
    assert_eq!(conf.input.type_name, "http_client");
    assert_eq!(conf.pipeline.processors.len(), 2);
    assert_eq!(conf.pipeline.processors[0].type_name, "filter");
    assert_eq!(conf.output.type_name, "stdout");
    assert_eq!(
        interval_from(&conf.input.config).unwrap(),
        Duration::from_secs(60)
    );
}

#[test]
fn pipeline_is_optional() {
    let conf = parse_stream(
        r#"
input:
  generate:
    json: {hello: world}
output:
  stdout: {}
"#,
    )
    .unwrap();
    assert!(conf.pipeline.processors.is_empty());
    assert_eq!(
        interval_from(&conf.input.config).unwrap(),
        Duration::from_secs(300),
        "default interval is 5m"
    );
}

#[test]
fn rejects_multi_type_component() {
    let result = parse_stream(
        r#"
input:
  http_client: {}
  redfish: {}
output:
  stdout: {}
"#,
    );
    assert!(result.is_err());
}

#[test]
fn expands_env_vars() {
    std::env::set_var("BENTO_RS_TEST_VAL", "secret");
    let out =
        expand_env("password: ${BENTO_RS_TEST_VAL} plain: $HOME missing: ${BENTO_RS_TEST_UNSET}!");
    assert_eq!(out, "password: secret plain: $HOME missing: !");
}

#[test]
fn parses_durations() {
    assert_eq!(parse_duration("30s").unwrap(), Duration::from_secs(30));
    assert_eq!(parse_duration("5m").unwrap(), Duration::from_secs(300));
    assert_eq!(parse_duration("250ms").unwrap(), Duration::from_millis(250));
    assert_eq!(parse_duration("0s").unwrap(), Duration::ZERO);
    assert!(parse_duration("nonsense").is_err());
}
