use anyhow::{anyhow, Context, Result};
use portable_pty::{CommandBuilder, MasterPty, NativePtySystem, PtySize, PtySystem};
use std::collections::{BTreeMap, BTreeSet};
use std::io::{Read, Write};
use std::sync::mpsc;
use std::thread;

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
    master: Box<dyn MasterPty + Send>,
    writer: Box<dyn Write + Send>,
    child: Box<dyn portable_pty::Child + Send>,
}

enum PtyEvent {
    Output { session_id: String, data: String },
    Exited { session_id: String },
}

pub struct PtyManager {
    sessions: BTreeMap<String, ManagedSession>,
    output_tx: mpsc::Sender<PtyEvent>,
    output_rx: mpsc::Receiver<PtyEvent>,
    pending_output: BTreeMap<String, Vec<String>>,
    exited_sessions: BTreeSet<String>,
}

impl PtyManager {
    pub fn new() -> Self {
        let (output_tx, output_rx) = mpsc::channel();
        Self {
            sessions: BTreeMap::new(),
            output_tx,
            output_rx,
            pending_output: BTreeMap::new(),
            exited_sessions: BTreeSet::new(),
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
        command.env("TERM", "xterm-256color");
        command.env("COLORTERM", "truecolor");
        let child = pair.slave.spawn_command(command).context("spawn shell")?;
        let pid = child.process_id().unwrap_or(0);
        let mut reader = pair.master.try_clone_reader().context("clone pty reader")?;
        let writer = pair.master.take_writer().context("take pty writer")?;
        let session_id = req.session_id.clone();
        let output_tx = self.output_tx.clone();
        thread::spawn(move || {
            let mut buf = [0_u8; 8192];
            loop {
                match reader.read(&mut buf) {
                    Ok(0) => break,
                    Ok(n) => {
                        let data = String::from_utf8_lossy(&buf[..n]).to_string();
                        if output_tx
                            .send(PtyEvent::Output {
                                session_id: session_id.clone(),
                                data,
                            })
                            .is_err()
                        {
                            break;
                        }
                    }
                    Err(_) => break,
                }
            }
            let _ = output_tx.send(PtyEvent::Exited { session_id });
        });
        self.sessions.insert(
            req.session_id.clone(),
            ManagedSession {
                master: pair.master,
                writer,
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
        session
            .writer
            .write_all(data.as_bytes())
            .context("write pty")
    }

    pub fn resize(&mut self, session_id: &str, cols: u16, rows: u16) -> Result<()> {
        let session = self
            .sessions
            .get_mut(session_id)
            .ok_or_else(|| anyhow!("session not found: {session_id}"))?;
        session
            .master
            .resize(PtySize {
                rows,
                cols,
                pixel_width: 0,
                pixel_height: 0,
            })
            .context("resize pty")
    }

    pub fn has_session(&self, session_id: &str) -> bool {
        self.sessions.contains_key(session_id)
    }

    pub fn read_available(&mut self, session_id: &str) -> Result<String> {
        if !self.sessions.contains_key(session_id) {
            return Err(anyhow!("session not found: {session_id}"));
        }
        let mut output = self
            .pending_output
            .remove(session_id)
            .unwrap_or_default()
            .join("");
        while let Ok(event) = self.output_rx.try_recv() {
            match event {
                PtyEvent::Output {
                    session_id: event_session_id,
                    data,
                } => {
                    if event_session_id == session_id {
                        output.push_str(&data);
                    } else {
                        self.pending_output
                            .entry(event_session_id)
                            .or_default()
                            .push(data);
                    }
                }
                PtyEvent::Exited { session_id } => {
                    if self.sessions.remove(&session_id).is_some() {
                        self.exited_sessions.insert(session_id);
                    }
                }
            }
        }
        Ok(output)
    }

    pub fn drain_exited(&mut self) -> Vec<String> {
        while let Ok(event) = self.output_rx.try_recv() {
            match event {
                PtyEvent::Output { session_id, data } => {
                    self.pending_output
                        .entry(session_id)
                        .or_default()
                        .push(data);
                }
                PtyEvent::Exited { session_id } => {
                    if self.sessions.remove(&session_id).is_some() {
                        self.exited_sessions.insert(session_id);
                    }
                }
            }
        }
        std::mem::take(&mut self.exited_sessions)
            .into_iter()
            .collect()
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
