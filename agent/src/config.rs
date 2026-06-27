use anyhow::{Context, Result};
use serde::{Deserialize, Serialize};
use std::fs;
use std::path::{Path, PathBuf};

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq)]
pub struct AgentConfig {
    pub server_url: String,
    pub device_id: String,
    pub credential: String,
    pub device_name: String,
}

pub fn default_config_path() -> Result<PathBuf> {
    let base = dirs::config_dir().context("config directory not found")?;
    Ok(base.join("vibe-terminal").join("agent.json"))
}

pub fn load(path: &Path) -> Result<AgentConfig> {
    let data = fs::read_to_string(path).with_context(|| format!("read {}", path.display()))?;
    serde_json::from_str(&data).with_context(|| format!("decode {}", path.display()))
}

pub fn save(path: &Path, config: &AgentConfig) -> Result<()> {
    if let Some(parent) = path.parent() {
        fs::create_dir_all(parent).with_context(|| format!("create {}", parent.display()))?;
    }
    let data = serde_json::to_string_pretty(config)?;
    fs::write(path, data).with_context(|| format!("write {}", path.display()))
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn save_and_load_config() {
        let dir = tempfile::tempdir().expect("tempdir");
        let path = dir.path().join("agent.json");
        let config = AgentConfig {
            server_url: "https://example.com".into(),
            device_id: "dev-1".into(),
            credential: "secret".into(),
            device_name: "laptop".into(),
        };
        save(&path, &config).expect("save");
        assert_eq!(load(&path).expect("load"), config);
    }
}
