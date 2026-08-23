# fish completions for strument.

set -l strument_commands trust history config model-config tool shell version
set -l strument_chat_commands __strument_chat_command

function __strument_no_subcommand
    not __fish_seen_subcommand_from $strument_commands
end

function __strument_chat_command
    commandline -opc | string match -q -r '^strument$'
end

function __strument_model_values
    strument config models 2>/dev/null
end

complete -c strument -f

# Top-level commands.
complete -c strument -n __strument_no_subcommand -a trust -d "Trust the project's .strument.star config file"
complete -c strument -n __strument_no_subcommand -a history -d "Print the path to this project's chat-history file"
complete -c strument -n __strument_no_subcommand -a config -d "Inspect the resolved config"
complete -c strument -n __strument_no_subcommand -a model-config -d "Print copy-pastable model config fetched from a provider"
complete -c strument -n __strument_no_subcommand -a tool -d "Run one observation tool and print what a model would see"
complete -c strument -n __strument_no_subcommand -a shell -d "Generate shell completions"
complete -c strument -n __strument_no_subcommand -a version -d "Print version and exit"

# Chat options.
complete -c strument -n __strument_chat_command -s m -l message -d "Send one message and exit" -r
complete -c strument -n __strument_chat_command -s c -l continue -d "Generate fresh notes from the previous transcript on startup"
complete -c strument -n __strument_chat_command -s M -l model -d "Model alias from config" -a "(__strument_model_values)"
complete -c strument -n __strument_chat_command -l no-git -d "Disable git integration"
complete -c strument -n __strument_chat_command -l no-color -d "Disable ANSI color and styling"
complete -c strument -n __strument_chat_command -l dark-mode -d "Use colors suited to a dark terminal background"
complete -c strument -n __strument_chat_command -l light-mode -d "Use colors suited to a light terminal background"
complete -c strument -n __strument_chat_command -l no-auto-commits -d "Keep git integration but do not auto-commit edits"
complete -c strument -n __strument_chat_command -l no-history -d "Do not write the session to the chat-history file"
complete -c strument -n __strument_chat_command --long jsonl -d "Also record the session to this file as JSONL" -r
complete -c strument -n __strument_chat_command --long dry-run -d "Report edits without writing files or committing"
complete -c strument -n __strument_chat_command --long yes -d "Answer yes to confirmations"
complete -c strument -n __strument_chat_command --long yes-shell -d "Also auto-run model-suggested shell commands"
complete -c strument -n __strument_chat_command --long version -d "Print version and exit"

# config.
complete -c strument -n "__fish_seen_subcommand_from config" -a "models default"

# model-config.
complete -c strument -n "__fish_seen_subcommand_from model-config" -s s -l source -d "Metadata source" -a openrouter
complete -c strument -n "__fish_seen_subcommand_from model-config" -l provider-name -d "Provider variable name emitted in the model call" -r
complete -c strument -n "__fish_seen_subcommand_from model-config" -l proxy -d "SOCKS5 proxy for the catalog fetch" -r
complete -c strument -n "__fish_seen_subcommand_from model-config" -a "model"

# tool.
complete -c strument -n "__fish_seen_subcommand_from tool" -s r -l root -d "Project root" -r
complete -c strument -n "__fish_seen_subcommand_from tool" -l json -d "Print tool arguments and result as JSON"
complete -c strument -n "__fish_seen_subcommand_from tool" -a "read grep glob ls symbol"
complete -c strument -n "__fish_seen_subcommand_from tool read" -a ""
complete -c strument -n "__fish_seen_subcommand_from tool grep" -l glob -d "Only search paths matching this glob" -r
complete -c strument -n "__fish_seen_subcommand_from tool grep" -l path -d "Only search under this directory" -r
complete -c strument -n "__fish_seen_subcommand_from tool grep" -l mode -d "What to return" -a "files content count"
complete -c strument -n "__fish_seen_subcommand_from tool grep" --long ignore-case -d "Match case-insensitively"
complete -c strument -n "__fish_seen_subcommand_from tool symbol" -l kind -d "Definition or references" -a "definition reference"

# shell.
complete -c strument -n "__fish_seen_subcommand_from shell" -a "bash fish"
