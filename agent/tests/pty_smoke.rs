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
    manager
        .write(&session.session_id, "printf hello\nexit\n")
        .expect("write");
    std::thread::sleep(Duration::from_millis(300));
    let output = manager.read_available(&session.session_id).expect("read");
    assert!(output.contains("hello"), "output was {output:?}");
}

#[test]
fn read_available_returns_quickly_when_no_output() {
    let mut manager = PtyManager::new();
    let session = manager
        .start(StartRequest {
            session_id: "sess-no-output".into(),
            shell_path: "/bin/cat".into(),
            working_directory: "/tmp".into(),
            cols: 80,
            rows: 24,
        })
        .expect("start pty");
    let session_id = session.session_id.clone();
    let (tx, rx) = std::sync::mpsc::channel();

    std::thread::spawn(move || {
        let result = manager.read_available(&session_id);
        tx.send(result).expect("send read result");
    });

    let output = rx
        .recv_timeout(Duration::from_millis(250))
        .expect("read_available should not block when no bytes are ready")
        .expect("read");
    assert_eq!(output, "");
}

#[test]
fn pty_sets_interactive_terminal_environment() {
    let previous_term = std::env::var("TERM").ok();
    std::env::set_var("TERM", "dumb");

    let mut manager = PtyManager::new();
    let session = manager
        .start(StartRequest {
            session_id: "sess-term".into(),
            shell_path: "/bin/sh".into(),
            working_directory: "/tmp".into(),
            cols: 80,
            rows: 24,
        })
        .expect("start pty");
    manager
        .write(
            &session.session_id,
            "printf '%s %s\\n' \"$TERM\" \"$COLORTERM\"\nexit\n",
        )
        .expect("write");
    std::thread::sleep(Duration::from_millis(300));
    let output = manager.read_available(&session.session_id).expect("read");

    match previous_term {
        Some(value) => std::env::set_var("TERM", value),
        None => std::env::remove_var("TERM"),
    }

    assert!(
        output.contains("xterm-256color truecolor"),
        "output was {output:?}"
    );
}

#[test]
fn pty_resize_updates_terminal_size() {
    let mut manager = PtyManager::new();
    let session = manager
        .start(StartRequest {
            session_id: "sess-resize".into(),
            shell_path: "/bin/sh".into(),
            working_directory: "/tmp".into(),
            cols: 80,
            rows: 24,
        })
        .expect("start pty");

    manager
        .resize(&session.session_id, 120, 40)
        .expect("resize pty");
    manager
        .write(&session.session_id, "stty size\nexit\n")
        .expect("write");
    std::thread::sleep(Duration::from_millis(300));
    let output = manager.read_available(&session.session_id).expect("read");

    assert!(output.contains("40 120"), "output was {output:?}");
}

#[test]
fn pty_reports_session_exit_after_child_finishes() {
    let mut manager = PtyManager::new();
    let session = manager
        .start(StartRequest {
            session_id: "sess-exit".into(),
            shell_path: "/bin/sh".into(),
            working_directory: "/tmp".into(),
            cols: 80,
            rows: 24,
        })
        .expect("start pty");

    manager
        .write(&session.session_id, "printf done\nexit\n")
        .expect("write");
    std::thread::sleep(Duration::from_millis(300));
    let _ = manager.read_available(&session.session_id).expect("read");

    assert_eq!(manager.drain_exited(), vec!["sess-exit".to_string()]);
}
