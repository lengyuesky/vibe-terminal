use anyhow::{anyhow, Context, Result};
use portable_pty::{CommandBuilder, NativePtySystem, PtySize, PtySystem};
use std::collections::BTreeMap;
use std::io::{Read, Write};
use std::thread;
use std::time::Duration;

#[derive(Debug, Clone)]
pub struct StartRequest {
    pub session_id: String,
    pub shell_path: String,
    pub working_directory: String,
    pub cols: u16,
    pub rows: u16,
}

#[derive(Debug, Clone)]
pub struct StartedSession {
    pub session_id: String,
    pub pid: u32,
}

struct ManagedSession {
    writer: Box<dyn Write + Send>,
    reader: Box<dyn Read + Send>,
    child: Box<dyn portable_pty::Child + Send>,
}

pub struct PtyManager {
    sessions: BTreeMap<String, ManagedSession>,
}

impl PtyManager {
    pub fn new() -> Self {
        Self {
            sessions: BTreeMap::new(),
        }
    }

    pub fn start(&mut self, req: StartRequest) -> Result<StartedSession> {
        let pty_system = NativePtySystem::default();
        let pair = pty_system
            .openpty(PtySize {
                rows: req.rows,
                cols: req.cols,
                pixel_width: 0,
                pixel_height: 0,
            })
            .context("open pty")?;
        let mut command = CommandBuilder::new(req.shell_path.clone());
        command.cwd(req.working_directory.clone());
        let child = pair.slave.spawn_command(command).context("spawn shell")?;
        let pid = child.process_id().unwrap_or(0);
        let reader = pair.master.try_clone_reader().context("clone pty reader")?;
        let writer = pair.master.take_writer().context("take pty writer")?;
        self.sessions.insert(
            req.session_id.clone(),
            ManagedSession {
                writer,
                reader,
                child,
            },
        );
        Ok(StartedSession {
            session_id: req.session_id,
            pid,
        })
    }

    pub fn write(&mut self, session_id: &str, data: &str) -> Result<()> {
        let session = self
            .sessions
            .get_mut(session_id)
            .ok_or_else(|| anyhow!("session not found: {session_id}"))?;
        session.writer.write_all(data.as_bytes()).context("write pty")
    }

    pub fn read_available(&mut self, session_id: &str) -> Result<String> {
        let session = self
            .sessions
            .get_mut(session_id)
            .ok_or_else(|| anyhow!("session not found: {session_id}"))?;
        thread::sleep(Duration::from_millis(50));
        let mut buf = [0_u8; 8192];
        let n = session.reader.read(&mut buf).unwrap_or(0);
        Ok(String::from_utf8_lossy(&buf[..n]).to_string())
    }

    pub fn close(&mut self, session_id: &str) -> Result<()> {
        if let Some(mut session) = self.sessions.remove(session_id) {
            let _ = session.child.kill();
        }
        Ok(())
    }
}

impl Default for PtyManager {
    fn default() -> Self {
        Self::new()
    }
}
