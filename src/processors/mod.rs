//! Built-in processors. Adding a new processor is one module here
//! (implement `Processor`, expose a `new` factory) plus one line in
//! `Registry::builtin`.

pub mod filter;
pub mod remove;
pub mod select;
pub mod set;
pub mod set_meta;

/// Looks up a dot-separated path (e.g. "status.phase") in a JSON value.
pub(crate) fn get_path<'a>(
    value: &'a serde_json::Value,
    path: &str,
) -> Option<&'a serde_json::Value> {
    let mut current = value;
    for segment in path.split('.') {
        current = current.get(segment)?;
    }
    Some(current)
}

/// Sets a dot-separated path in a JSON value, creating intermediate objects.
pub(crate) fn set_path(value: &mut serde_json::Value, path: &str, new: serde_json::Value) {
    let mut current = value;
    let segments: Vec<&str> = path.split('.').collect();
    for (i, segment) in segments.iter().enumerate() {
        if !current.is_object() {
            *current = serde_json::json!({});
        }
        let map = current.as_object_mut().expect("just ensured object");
        if i == segments.len() - 1 {
            map.insert(segment.to_string(), new);
            return;
        }
        current = map
            .entry(segment.to_string())
            .or_insert_with(|| serde_json::json!({}));
    }
}

/// Removes a dot-separated path from a JSON value.
pub(crate) fn remove_path(value: &mut serde_json::Value, path: &str) {
    let (parent_path, leaf) = match path.rsplit_once('.') {
        Some((parent, leaf)) => (Some(parent), leaf),
        None => (None, path),
    };
    let target = match parent_path {
        Some(parent) => {
            let mut current = Some(value);
            for segment in parent.split('.') {
                current = current.and_then(|v| v.get_mut(segment));
            }
            current
        }
        None => Some(value),
    };
    if let Some(serde_json::Value::Object(map)) = target {
        map.remove(leaf);
    }
}
