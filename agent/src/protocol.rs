use serde::{Deserialize, Serialize};

pub const PROTOCOL_VERSION: &str = "v1";

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq)]
pub struct Envelope<T> {
    #[serde(rename = "type")]
    pub message_type: String,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub request_id: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub session_id: Option<String>,
    pub payload: T,
}

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq)]
pub struct StartSession {
    pub session_id: String,
    pub shell_path: String,
    pub working_directory: String,
    pub cols: u16,
    pub rows: u16,
}

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq)]
pub struct SessionStarted {
    pub session_id: String,
    pub agent_pid: u32,
    pub title: String,
    pub last_output_seq: i64,
}

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq)]
pub struct Stdin {
    pub session_id: String,
    pub data: String,
}

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq)]
pub struct Resize {
    pub session_id: String,
    pub cols: u16,
    pub rows: u16,
}

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq)]
pub struct Stdout {
    pub session_id: String,
    pub seq: i64,
    pub data: String,
}

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq)]
pub struct CloseSession {
    pub session_id: String,
}

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq)]
pub struct SessionExit {
    pub session_id: String,
    pub exit_code: i32,
    pub message: String,
}

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq)]
pub struct FsList {
    pub path: String,
}

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq)]
pub struct FsEntry {
    pub name: String,
    pub is_dir: bool,
    pub size: u64,
    pub mode: u32,
    pub modified_at: i64,
}

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq)]
pub struct FsListResult {
    pub path: String,
    pub entries: Vec<FsEntry>,
}

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq)]
pub struct FsRead {
    pub path: String,
    pub offset: u64,
    pub length: u32,
}

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq)]
pub struct FsReadResult {
    pub data: String,
    pub eof: bool,
    pub file_size: u64,
}

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq)]
pub struct FsWriteOpen {
    pub upload_id: String,
    pub path: String,
    pub size: u64,
    pub overwrite: bool,
}

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq)]
pub struct FsWriteOpened {
    pub upload_id: String,
}

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq)]
pub struct FsWriteChunk {
    pub upload_id: String,
    pub offset: u64,
    pub data: String,
}

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq)]
pub struct FsWriteAck {
    pub upload_id: String,
    pub offset: u64,
}

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq)]
pub struct FsWriteClose {
    pub upload_id: String,
    pub total_size: u64,
}

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq)]
pub struct FsWriteResult {
    pub upload_id: String,
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn start_session_round_trip() {
        let message = Envelope {
            message_type: "start_session".to_string(),
            request_id: None,
            session_id: Some("sess-1".to_string()),
            payload: StartSession {
                session_id: "sess-1".to_string(),
                shell_path: "/bin/bash".to_string(),
                working_directory: "/home/dev".to_string(),
                cols: 120,
                rows: 32,
            },
        };
        let json = serde_json::to_string(&message).expect("serialize");
        let decoded: Envelope<StartSession> = serde_json::from_str(&json).expect("decode");
        assert_eq!(decoded.payload.session_id, "sess-1");
        assert_eq!(decoded.payload.cols, 120);
    }

    #[test]
    fn envelope_decodes_request_id() {
        let json = r#"{"type":"fs_list","request_id":"req-1","payload":{"path":"/tmp"}}"#;
        let envelope: Envelope<FsList> = serde_json::from_str(json).expect("decode");
        assert_eq!(envelope.request_id.as_deref(), Some("req-1"));
        assert_eq!(envelope.payload.path, "/tmp");
    }

    #[test]
    fn envelope_without_request_id_still_decodes() {
        let json = r#"{"type":"stdin","session_id":"s1","payload":{"session_id":"s1","data":"ls"}}"#;
        let envelope: Envelope<Stdin> = serde_json::from_str(json).expect("decode");
        assert!(envelope.request_id.is_none());
    }

    #[test]
    fn fs_read_result_round_trip() {
        let value = FsReadResult { data: "aGk=".into(), eof: true, file_size: 2 };
        let json = serde_json::to_string(&value).expect("serialize");
        assert!(json.contains("\"file_size\":2"));
        let back: FsReadResult = serde_json::from_str(&json).expect("deserialize");
        assert_eq!(back, value);
    }
}
