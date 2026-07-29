use anyhow::Result;
use async_trait::async_trait;
use tokio::io::AsyncWriteExt;

use crate::message::Message;
use crate::registry::Destination;

/// Writes each message as a JSON line to standard output.
struct StdoutDestination;

pub fn new(_raw: &serde_yaml::Value) -> Result<Box<dyn Destination>> {
    Ok(Box::new(StdoutDestination))
}

#[async_trait]
impl Destination for StdoutDestination {
    async fn write(&self, batch: &[Message]) -> Result<()> {
        let mut out = tokio::io::stdout();
        for msg in batch {
            let mut line = serde_json::to_vec(msg)?;
            line.push(b'\n');
            out.write_all(&line).await?;
        }
        out.flush().await?;
        Ok(())
    }
}
