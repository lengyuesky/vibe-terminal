use anyhow::Result;
use clap::{Parser, Subcommand};
use vibe_agent::client::{register_agent, run_control_loop};
use vibe_agent::config;
use vibe_agent::registry::SessionRegistry;

#[derive(Parser, Debug)]
#[command(name = "vibe-agent")]
struct Cli {
    #[command(subcommand)]
    command: Command,
}

#[derive(Subcommand, Debug)]
enum Command {
    Run,
    Register {
        #[arg(long)]
        server: String,
        #[arg(long)]
        token: String,
    },
}

#[tokio::main]
async fn main() -> Result<()> {
    let cli = Cli::parse();
    match cli.command {
        Command::Register { server, token } => {
            let device_name = device_name();
            let config = register_agent(&server, &token, &device_name).await?;
            let path = config::default_config_path()?;
            config::save(&path, &config)?;
            println!(
                "registered device {} at {}",
                config.device_id,
                path.display()
            );
        }
        Command::Run => {
            let path = config::default_config_path()?;
            let config = config::load(&path)?;
            let mut registry = SessionRegistry::default();
            registry.mark_lost_after_restart();
            run_control_loop(config, registry).await?;
        }
    }
    Ok(())
}

fn device_name() -> String {
    std::env::var("HOSTNAME")
        .or_else(|_| std::env::var("COMPUTERNAME"))
        .unwrap_or_else(|_| "vibe-agent".to_string())
}
