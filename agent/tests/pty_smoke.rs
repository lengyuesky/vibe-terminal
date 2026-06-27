use std::time::Duration;

use vibe_agent::pty_manager::{PtyManager, StartRequest};

#[test]
fn pty_runs_shell_command() {
    let mut manager = PtyManager::new();
    let session = manager
        .start(StartRequest {
            session_id: "sess-1".into(),
            shell_path: "/bin/sh".into(),
            working_directory: "/tmp".into(),
            cols: 80,
            rows: 24,
        })
        .expect("start pty");
    manager.write(&session.session_id, "printf hello\nexit\n").expect("write");
    std::thread::sleep(Duration::from_millis(300));
    let output = manager.read_available(&session.session_id).expect("read");
    assert!(output.contains("hello"), "output was {output:?}");
}
