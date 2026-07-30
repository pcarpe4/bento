use anyhow::{bail, Context, Result};
use async_trait::async_trait;
use mongodb::bson::{self, Bson};
use serde::Deserialize;
use tokio::sync::OnceCell;

use crate::message::Message;
use crate::registry::Output;

/// Inserts each message's data as a document, with the collection metadata
/// stored under "_meta".
#[derive(Deserialize)]
struct MongoConfig {
    url: String,
    database: String,
    collection: String,
    #[serde(default)]
    username: String,
    #[serde(default)]
    password: String,
}

struct MongoDestination {
    cfg: MongoConfig,
    client: OnceCell<mongodb::Client>,
}

pub fn new(raw: &serde_yaml::Value) -> Result<Box<dyn Output>> {
    let cfg: MongoConfig = serde_yaml::from_value(raw.clone()).context("invalid config")?;
    for (value, field) in [
        (&cfg.url, "url"),
        (&cfg.database, "database"),
        (&cfg.collection, "collection"),
    ] {
        if value.is_empty() {
            bail!("config field {field:?} is required");
        }
    }
    Ok(Box::new(MongoDestination {
        cfg,
        client: OnceCell::new(),
    }))
}

impl MongoDestination {
    async fn client(&self) -> Result<&mongodb::Client> {
        self.client
            .get_or_try_init(|| async {
                let mut options = mongodb::options::ClientOptions::parse(&self.cfg.url)
                    .await
                    .context("failed to parse mongodb url")?;
                if !self.cfg.username.is_empty() {
                    options.credential = Some(
                        mongodb::options::Credential::builder()
                            .username(self.cfg.username.clone())
                            .password(self.cfg.password.clone())
                            .build(),
                    );
                }
                mongodb::Client::with_options(options).context("failed to create mongodb client")
            })
            .await
    }
}

#[async_trait]
impl Output for MongoDestination {
    async fn write(&self, batch: &[Message]) -> Result<()> {
        let mut docs = Vec::with_capacity(batch.len());
        for msg in batch {
            let mut doc = match bson::to_bson(&msg.data).context("failed to convert message")? {
                Bson::Document(doc) => doc,
                other => bson::doc! { "value": other },
            };
            doc.insert("_meta", bson::to_bson(&msg.meta)?);
            docs.push(doc);
        }

        self.client()
            .await?
            .database(&self.cfg.database)
            .collection::<bson::Document>(&self.cfg.collection)
            .insert_many(docs)
            .await
            .context("mongodb insert failed")?;
        Ok(())
    }

    async fn close(&self) -> Result<()> {
        if let Some(client) = self.client.get() {
            client.clone().shutdown().await;
        }
        Ok(())
    }
}
