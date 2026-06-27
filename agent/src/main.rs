mod buffer;
mod config;
mod protocol;
mod registry;

use clap::{Parser, Subcommand};

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

fn main() {
    let cli = Cli::parse();
    println!("{:?}", cli.command);
}
