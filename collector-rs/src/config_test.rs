use std::time::Duration;

use super::config::{expand_env, parse, parse_duration};

#[test]
fn parses_valid_config_with_defaults() {
    let conf = parse(
        r#"
sources:
  - name: a
    type: http_api
    destinations: [out]
    config:
      urls: [https://example.com]
destinations:
  - name: out
    type: stdout
"#,
    )
    .unwrap();
    assert_eq!(conf.default_interval, "5m");
    assert_eq!(conf.sources.len(), 1);
    assert_eq!(conf.sources[0].destinations, vec!["out"]);
}

#[test]
fn rejects_invalid_configs() {
    for bad in [
        "destinations: [{name: out, type: stdout}]",
        "sources: [{name: a, type: t}]",
        "sources: [{name: a, type: t, destinations: [nope]}]\ndestinations: [{name: out, type: t}]",
        "sources: [{name: a, type: t}, {name: a, type: t}]\ndestinations: [{name: out, type: t}]",
        "sources: [{name: a, type: t, interval: nonsense}]\ndestinations: [{name: out, type: t}]",
    ] {
        assert!(parse(bad).is_err(), "expected error for: {bad}");
    }
}

#[test]
fn expands_env_vars() {
    std::env::set_var("COLLECTOR_TEST_VAL", "secret");
    let out = expand_env(
        "password: ${COLLECTOR_TEST_VAL} plain: $HOME missing: ${COLLECTOR_TEST_UNSET_VAL}!",
    );
    assert_eq!(out, "password: secret plain: $HOME missing: !");
}

#[test]
fn parses_durations() {
    assert_eq!(parse_duration("30s").unwrap(), Duration::from_secs(30));
    assert_eq!(parse_duration("5m").unwrap(), Duration::from_secs(300));
    assert_eq!(parse_duration("1h").unwrap(), Duration::from_secs(3600));
    assert_eq!(parse_duration("250ms").unwrap(), Duration::from_millis(250));
    assert_eq!(parse_duration("0s").unwrap(), Duration::ZERO);
    assert!(parse_duration("nonsense").is_err());
    assert!(parse_duration("30").is_err());
}
