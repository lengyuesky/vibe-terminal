use anyhow::{Context, Result};
use serde::{Deserialize, Serialize};
use std::collections::BTreeMap;
use std::fs;
use std::path::Path;

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq)]
pub struct SessionRecord {
    pub session_id: String,
    pub title: String,
    pub shell_path: String,
    pub working_directory: String,
    pub status: String,
    pub agent_pid: u32,
    pub last_output_seq: i64,
}

#[derive(Debug, Default, Clone, Serialize, Deserialize)]
pub struct SessionRegistry {
    sessions: BTreeMap<String, SessionRecord>,
}

impl SessionRegistry {
    pub fn upsert(&mut self, record: SessionRecord) {
        self.sessions.insert(record.session_id.clone(), record);
    }

    pub fn mark_lost_after_restart(&mut self) {
        for record in self.sessions.values_mut() {
            if record.status == "running" || record.status == "starting" {
                record.status = "lost".to_string();
            }
        }
    }

    pub fn list(&self) -> Vec<SessionRecord> {
        self.sessions.values().cloned().collect()
    }

    pub fn save(&self, path: &Path) -> Result<()> {
        if let Some(parent) = path.parent() {
            fs::create_dir_all(parent).with_context(|| format!("create {}", parent.display()))?;
        }
        fs::write(path, serde_json::to_string_pretty(self)?)
            .with_context(|| format!("write {}", path.display()))
    }

    pub fn load(path: &Path) -> Result<Self> {
        if !path.exists() {
            return Ok(Self::default());
        }
        let data = fs::read_to_string(path).with_context(|| format!("read {}", path.display()))?;
        serde_json::from_str(&data).with_context(|| format!("decode {}", path.display()))
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn mark_running_sessions_lost_after_restart() {
        let mut registry = SessionRegistry::default();
        registry.upsert(SessionRecord {
            session_id: "sess-1".into(),
            title: "bash".into(),
            shell_path: "/bin/bash".into(),
            working_directory: "/home/dev".into(),
            status: "running".into(),
            agent_pid: 123,
            last_output_seq: 9,
        });
        registry.mark_lost_after_restart();
        assert_eq!(registry.list()[0].status, "lost");
    }
}
