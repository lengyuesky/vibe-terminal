use base64::engine::general_purpose::STANDARD as BASE64;
use base64::Engine;
use serde_json::Value;
use std::collections::HashMap;
use std::fs::{self, File, OpenOptions};
use std::io::{Read, Seek, SeekFrom, Write};
use std::path::{Path, PathBuf};
use std::time::{Duration, Instant};

use crate::protocol::{
    FsEntry, FsList, FsListResult, FsRead, FsReadResult, FsWriteAck, FsWriteChunk, FsWriteClose,
    FsWriteOpen, FsWriteOpened, FsWriteResult,
};

// 拒绝异常超大块，防止内存滥用。
pub const MAX_CHUNK_LEN: u32 = 1024 * 1024;

#[derive(Debug)]
pub enum FsOpError {
    NotFound(String),
    PermissionDenied(String),
    NotAFile(String),
    NotADirectory(String),
    AlreadyExists(String),
    InvalidPath(String),
    InvalidRequest(String),
    Io(String),
}

impl FsOpError {
    pub fn code(&self) -> &'static str {
        match self {
            FsOpError::NotFound(_) => "not_found",
            FsOpError::PermissionDenied(_) => "permission_denied",
            FsOpError::NotAFile(_) => "not_a_file",
            FsOpError::NotADirectory(_) => "not_a_directory",
            FsOpError::AlreadyExists(_) => "already_exists",
            FsOpError::InvalidPath(_) => "invalid_path",
            FsOpError::InvalidRequest(_) => "invalid_request",
            FsOpError::Io(_) => "io_error",
        }
    }

    pub fn message(&self) -> &str {
        match self {
            FsOpError::NotFound(m)
            | FsOpError::PermissionDenied(m)
            | FsOpError::NotAFile(m)
            | FsOpError::NotADirectory(m)
            | FsOpError::AlreadyExists(m)
            | FsOpError::InvalidPath(m)
            | FsOpError::InvalidRequest(m)
            | FsOpError::Io(m) => m,
        }
    }
}

fn io_error(path: &Path, err: std::io::Error) -> FsOpError {
    match err.kind() {
        std::io::ErrorKind::NotFound => {
            FsOpError::NotFound(format!("{} not found", path.display()))
        }
        std::io::ErrorKind::PermissionDenied => {
            FsOpError::PermissionDenied(format!("permission denied for {}", path.display()))
        }
        _ => FsOpError::Io(format!("{}: {}", path.display(), err)),
    }
}

fn resolve_path(raw: &str) -> Result<PathBuf, FsOpError> {
    if raw == "~" {
        return dirs::home_dir()
            .ok_or_else(|| FsOpError::InvalidPath("home directory unavailable".into()));
    }
    if let Some(rest) = raw.strip_prefix("~/") {
        let home = dirs::home_dir()
            .ok_or_else(|| FsOpError::InvalidPath("home directory unavailable".into()))?;
        return Ok(home.join(rest));
    }
    let path = PathBuf::from(raw);
    if !path.is_absolute() {
        return Err(FsOpError::InvalidPath(format!(
            "path must be absolute: {raw}"
        )));
    }
    Ok(path)
}

pub fn list_dir(raw_path: &str) -> Result<FsListResult, FsOpError> {
    let path = resolve_path(raw_path)?;
    let meta = fs::metadata(&path).map_err(|e| io_error(&path, e))?;
    if !meta.is_dir() {
        return Err(FsOpError::NotADirectory(format!(
            "{} is not a directory",
            path.display()
        )));
    }
    let mut entries = Vec::new();
    for entry in fs::read_dir(&path).map_err(|e| io_error(&path, e))? {
        let entry = entry.map_err(|e| io_error(&path, e))?;
        let name = entry.file_name().to_string_lossy().to_string();
        // 损坏的符号链接读不到 metadata 时跳过，不让整个列表失败。
        let Ok(meta) = entry.metadata() else {
            continue;
        };
        entries.push(FsEntry {
            name,
            is_dir: meta.is_dir(),
            size: meta.len(),
            mode: file_mode(&meta),
            modified_at: modified_unix(&meta),
        });
    }
    entries.sort_by(|a, b| b.is_dir.cmp(&a.is_dir).then_with(|| a.name.cmp(&b.name)));
    Ok(FsListResult {
        path: path.to_string_lossy().to_string(),
        entries,
    })
}

#[cfg(unix)]
fn file_mode(meta: &fs::Metadata) -> u32 {
    use std::os::unix::fs::PermissionsExt;
    meta.permissions().mode()
}

#[cfg(not(unix))]
fn file_mode(_meta: &fs::Metadata) -> u32 {
    0
}

fn modified_unix(meta: &fs::Metadata) -> i64 {
    meta.modified()
        .ok()
        .and_then(|t| t.duration_since(std::time::UNIX_EPOCH).ok())
        .map(|d| d.as_secs() as i64)
        .unwrap_or(0)
}

pub fn read_chunk(raw_path: &str, offset: u64, length: u32) -> Result<FsReadResult, FsOpError> {
    if length == 0 || length > MAX_CHUNK_LEN {
        return Err(FsOpError::InvalidRequest(format!(
            "invalid chunk length {length}"
        )));
    }
    let path = resolve_path(raw_path)?;
    let meta = fs::metadata(&path).map_err(|e| io_error(&path, e))?;
    if meta.is_dir() {
        return Err(FsOpError::NotAFile(format!(
            "{} is a directory",
            path.display()
        )));
    }
    let mut file = File::open(&path).map_err(|e| io_error(&path, e))?;
    file.seek(SeekFrom::Start(offset))
        .map_err(|e| io_error(&path, e))?;
    let mut buf = vec![0u8; length as usize];
    let mut total = 0usize;
    loop {
        let n = file
            .read(&mut buf[total..])
            .map_err(|e| io_error(&path, e))?;
        if n == 0 {
            break;
        }
        total += n;
        if total == buf.len() {
            break;
        }
    }
    buf.truncate(total);
    Ok(FsReadResult {
        data: BASE64.encode(&buf),
        eof: total < length as usize,
        file_size: meta.len(),
    })
}

struct Upload {
    file: File,
    tmp_path: PathBuf,
    target: PathBuf,
    written: u64,
    last_activity: Instant,
}

pub struct UploadManager {
    uploads: HashMap<String, Upload>,
}

impl Default for UploadManager {
    fn default() -> Self {
        Self::new()
    }
}

impl UploadManager {
    pub fn new() -> Self {
        Self {
            uploads: HashMap::new(),
        }
    }

    pub fn open(
        &mut self,
        upload_id: &str,
        raw_path: &str,
        overwrite: bool,
    ) -> Result<(), FsOpError> {
        let target = resolve_path(raw_path)?;
        let Some(parent) = target.parent().map(Path::to_path_buf) else {
            return Err(FsOpError::InvalidPath(
                "upload target has no parent directory".into(),
            ));
        };
        let parent_meta = fs::metadata(&parent).map_err(|e| io_error(&parent, e))?;
        if !parent_meta.is_dir() {
            return Err(FsOpError::NotADirectory(format!(
                "{} is not a directory",
                parent.display()
            )));
        }
        if target.is_dir() {
            return Err(FsOpError::NotAFile(format!(
                "{} is a directory",
                target.display()
            )));
        }
        if !overwrite && target.exists() {
            return Err(FsOpError::AlreadyExists(format!(
                "{} already exists",
                target.display()
            )));
        }
        let tmp_path = parent.join(format!(".vibe-upload-{upload_id}.tmp"));
        let file = OpenOptions::new()
            .create_new(true)
            .write(true)
            .open(&tmp_path)
            .map_err(|e| io_error(&tmp_path, e))?;
        self.uploads.insert(
            upload_id.to_string(),
            Upload {
                file,
                tmp_path,
                target,
                written: 0,
                last_activity: Instant::now(),
            },
        );
        Ok(())
    }

    pub fn write_chunk(
        &mut self,
        upload_id: &str,
        offset: u64,
        data: &[u8],
    ) -> Result<u64, FsOpError> {
        let result = {
            let Some(upload) = self.uploads.get_mut(upload_id) else {
                return Err(FsOpError::NotFound(format!("upload {upload_id} not found")));
            };
            if offset != upload.written {
                Err(FsOpError::InvalidRequest(format!(
                    "unexpected offset {offset}, expected {}",
                    upload.written
                )))
            } else if let Err(err) = upload.file.write_all(data) {
                Err(FsOpError::Io(format!(
                    "{}: {}",
                    upload.tmp_path.display(),
                    err
                )))
            } else {
                upload.written += data.len() as u64;
                upload.last_activity = Instant::now();
                Ok(upload.written)
            }
        };
        if result.is_err() {
            self.abort(upload_id);
        }
        result
    }

    pub fn close(&mut self, upload_id: &str, total_size: u64) -> Result<(), FsOpError> {
        let Some(upload) = self.uploads.remove(upload_id) else {
            return Err(FsOpError::NotFound(format!("upload {upload_id} not found")));
        };
        if upload.written != total_size {
            let _ = fs::remove_file(&upload.tmp_path);
            return Err(FsOpError::InvalidRequest(format!(
                "size mismatch: wrote {}, expected {total_size}",
                upload.written
            )));
        }
        if let Err(err) = upload.file.sync_all() {
            let code = io_error(&upload.tmp_path, err);
            let _ = fs::remove_file(&upload.tmp_path);
            return Err(code);
        }
        drop(upload.file);
        if let Err(err) = fs::rename(&upload.tmp_path, &upload.target) {
            let code = io_error(&upload.target, err);
            let _ = fs::remove_file(&upload.tmp_path);
            return Err(code);
        }
        Ok(())
    }

    pub fn abort(&mut self, upload_id: &str) {
        if let Some(upload) = self.uploads.remove(upload_id) {
            drop(upload.file);
            let _ = fs::remove_file(&upload.tmp_path);
        }
    }

    pub fn cleanup_stale(&mut self, max_age: Duration) -> usize {
        let stale: Vec<String> = self
            .uploads
            .iter()
            .filter(|(_, upload)| upload.last_activity.elapsed() >= max_age)
            .map(|(id, _)| id.clone())
            .collect();
        for id in &stale {
            self.abort(id);
        }
        stale.len()
    }
}

pub fn handle_fs_message(
    uploads: &mut UploadManager,
    message_type: &str,
    payload: Value,
) -> Option<(String, Value)> {
    match message_type {
        "fs_list" => Some(reply(
            decode::<FsList>(payload).and_then(|req| list_dir(&req.path)),
            "fs_list_result",
        )),
        "fs_read" => Some(reply(
            decode::<FsRead>(payload).and_then(|req| read_chunk(&req.path, req.offset, req.length)),
            "fs_read_result",
        )),
        "fs_write_open" => Some(reply(
            decode::<FsWriteOpen>(payload).and_then(|req| {
                uploads
                    .open(&req.upload_id, &req.path, req.overwrite)
                    .map(|_| FsWriteOpened {
                        upload_id: req.upload_id,
                    })
            }),
            "fs_write_opened",
        )),
        "fs_write_chunk" => Some(reply(
            decode::<FsWriteChunk>(payload).and_then(|req| {
                let data = BASE64
                    .decode(&req.data)
                    .map_err(|_| FsOpError::InvalidRequest("invalid chunk encoding".into()))?;
                uploads
                    .write_chunk(&req.upload_id, req.offset, &data)
                    .map(|offset| FsWriteAck {
                        upload_id: req.upload_id,
                        offset,
                    })
            }),
            "fs_write_ack",
        )),
        "fs_write_close" => Some(reply(
            decode::<FsWriteClose>(payload).and_then(|req| {
                uploads
                    .close(&req.upload_id, req.total_size)
                    .map(|_| FsWriteResult {
                        upload_id: req.upload_id,
                    })
            }),
            "fs_write_result",
        )),
        _ => None,
    }
}

fn decode<T: serde::de::DeserializeOwned>(payload: Value) -> Result<T, FsOpError> {
    serde_json::from_value(payload)
        .map_err(|e| FsOpError::InvalidRequest(format!("invalid payload: {e}")))
}

fn reply<T: serde::Serialize>(result: Result<T, FsOpError>, ok_type: &str) -> (String, Value) {
    match result {
        Ok(value) => (
            ok_type.to_string(),
            serde_json::to_value(value).unwrap_or(Value::Null),
        ),
        Err(err) => (
            "fs_error".to_string(),
            serde_json::json!({"code": err.code(), "message": err.message()}),
        ),
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::fs as stdfs;
    use std::time::Duration;

    #[test]
    fn list_dir_sorts_directories_first() {
        let dir = tempfile::tempdir().expect("tempdir");
        stdfs::write(dir.path().join("b.txt"), b"hello").expect("write");
        stdfs::create_dir(dir.path().join("a_dir")).expect("mkdir");
        let result = list_dir(dir.path().to_str().unwrap()).expect("list");
        assert_eq!(result.entries[0].name, "a_dir");
        assert!(result.entries[0].is_dir);
        assert_eq!(result.entries[1].name, "b.txt");
        assert_eq!(result.entries[1].size, 5);
    }

    #[test]
    fn list_dir_rejects_relative_path() {
        let err = list_dir("relative/path").unwrap_err();
        assert_eq!(err.code(), "invalid_path");
    }

    #[test]
    fn list_dir_missing_path_is_not_found() {
        let err = list_dir("/definitely/missing/dir-xyz").unwrap_err();
        assert_eq!(err.code(), "not_found");
    }

    #[test]
    fn read_chunk_respects_offset_and_eof() {
        let dir = tempfile::tempdir().expect("tempdir");
        let path = dir.path().join("data.bin");
        stdfs::write(&path, b"hello world!").expect("write");
        let first = read_chunk(path.to_str().unwrap(), 0, 5).expect("read");
        assert_eq!(first.data, base64_encode(b"hello"));
        assert!(!first.eof);
        assert_eq!(first.file_size, 12);
        let last = read_chunk(path.to_str().unwrap(), 10, 5).expect("read tail");
        assert_eq!(last.data, base64_encode(b"d!"));
        assert!(last.eof);
    }

    #[test]
    fn read_chunk_on_directory_is_not_a_file() {
        let dir = tempfile::tempdir().expect("tempdir");
        let err = read_chunk(dir.path().to_str().unwrap(), 0, 4).unwrap_err();
        assert_eq!(err.code(), "not_a_file");
    }

    #[test]
    fn upload_full_cycle_renames_into_place() {
        let dir = tempfile::tempdir().expect("tempdir");
        let target = dir.path().join("upload.bin");
        let mut uploads = UploadManager::new();
        uploads
            .open("up-1", target.to_str().unwrap(), false)
            .expect("open");
        uploads.write_chunk("up-1", 0, b"hello ").expect("chunk 1");
        uploads.write_chunk("up-1", 6, b"world!").expect("chunk 2");
        uploads.close("up-1", 12).expect("close");
        assert_eq!(stdfs::read(&target).expect("read"), b"hello world!");
        assert!(!dir.path().join(".vibe-upload-up-1.tmp").exists());
    }

    #[test]
    fn upload_without_overwrite_rejects_existing_target() {
        let dir = tempfile::tempdir().expect("tempdir");
        let target = dir.path().join("exists.txt");
        stdfs::write(&target, b"old").expect("write");
        let mut uploads = UploadManager::new();
        let err = uploads
            .open("up-2", target.to_str().unwrap(), false)
            .unwrap_err();
        assert_eq!(err.code(), "already_exists");
    }

    #[test]
    fn upload_wrong_offset_aborts_and_cleans_tmp() {
        let dir = tempfile::tempdir().expect("tempdir");
        let target = dir.path().join("bad.bin");
        let mut uploads = UploadManager::new();
        uploads
            .open("up-3", target.to_str().unwrap(), false)
            .expect("open");
        let err = uploads.write_chunk("up-3", 5, b"data").unwrap_err();
        assert_eq!(err.code(), "invalid_request");
        assert!(!dir.path().join(".vibe-upload-up-3.tmp").exists());
        let err = uploads.write_chunk("up-3", 0, b"data").unwrap_err();
        assert_eq!(err.code(), "not_found");
    }

    #[test]
    fn close_with_size_mismatch_fails_and_cleans() {
        let dir = tempfile::tempdir().expect("tempdir");
        let target = dir.path().join("short.bin");
        let mut uploads = UploadManager::new();
        uploads
            .open("up-4", target.to_str().unwrap(), false)
            .expect("open");
        uploads.write_chunk("up-4", 0, b"abc").expect("chunk");
        let err = uploads.close("up-4", 99).unwrap_err();
        assert_eq!(err.code(), "invalid_request");
        assert!(!target.exists());
    }

    #[test]
    fn cleanup_stale_removes_idle_uploads() {
        let dir = tempfile::tempdir().expect("tempdir");
        let target = dir.path().join("stale.bin");
        let mut uploads = UploadManager::new();
        uploads
            .open("up-5", target.to_str().unwrap(), false)
            .expect("open");
        assert_eq!(uploads.cleanup_stale(Duration::from_secs(3600)), 0);
        assert_eq!(uploads.cleanup_stale(Duration::ZERO), 1);
        assert!(!dir.path().join(".vibe-upload-up-5.tmp").exists());
    }

    #[test]
    fn handle_fs_message_dispatches_list() {
        let dir = tempfile::tempdir().expect("tempdir");
        stdfs::write(dir.path().join("x.txt"), b"x").expect("write");
        let mut uploads = UploadManager::new();
        let payload = serde_json::json!({"path": dir.path().to_str().unwrap()});
        let (reply_type, reply) =
            handle_fs_message(&mut uploads, "fs_list", payload).expect("handled");
        assert_eq!(reply_type, "fs_list_result");
        assert_eq!(reply["entries"][0]["name"], "x.txt");
    }

    #[test]
    fn handle_fs_message_returns_error_payload() {
        let mut uploads = UploadManager::new();
        let payload = serde_json::json!({"path": "/missing-dir-xyz"});
        let (reply_type, reply) =
            handle_fs_message(&mut uploads, "fs_list", payload).expect("handled");
        assert_eq!(reply_type, "fs_error");
        assert_eq!(reply["code"], "not_found");
    }

    #[test]
    fn handle_fs_message_ignores_unknown_types() {
        let mut uploads = UploadManager::new();
        assert!(handle_fs_message(&mut uploads, "stdin", serde_json::json!({})).is_none());
    }

    fn base64_encode(data: &[u8]) -> String {
        use base64::Engine;
        base64::engine::general_purpose::STANDARD.encode(data)
    }
}
