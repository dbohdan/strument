# fish completions for strument.
#
# Verified by driving `complete -C` against fish 3.7, not by reading. Two of
# fish's rules this file got wrong for a long time, and must keep getting right:
#
#   - A `set -l` at file scope is NOT visible inside a function body when that
#     function runs during completion. The lists below are expanded into each
#     condition string at load time, which is why they work. A helper function
#     that read $commands saw an empty list, so `not __fish_seen_subcommand_from
#     $commands` was always true and every top-level command was offered after
#     every subcommand — including in the middle of `tool grep --mode `.
#   - An option that takes a value needs -r, or -x for -r plus no files.
#     Without it fish does not know there is an argument to complete and never
#     reaches the option's own -a candidates: that is why -M offered the command
#     list instead of model aliases.
#
# cmd/strument/completions_test.go holds the command and flag names here to the
# ones kong actually parses, in both directions. It was one-directional once,
# and three names that no longer existed survived in this file for months.

# chat is a command you can name as well as the default one, so it is offered
# like the rest but does not end chat's own flags.
set -l subcommands trust history config model-config project session tool shell
set -l commands chat $subcommands

function __strument_models
    strument config models 2>/dev/null
end

# No file completion by default. The places that take a path ask for it.
complete -c strument -f

# Top-level commands, only while none has been chosen.
complete -c strument -n __fish_use_subcommand -a chat -d "Chat with a model about the given files (the default)"
complete -c strument -n __fish_use_subcommand -a trust -d "Trust the project's config file and its skills"
complete -c strument -n __fish_use_subcommand -a history -d "Inspect or edit this project's chat-history file"
complete -c strument -n __fish_use_subcommand -a config -d "Inspect the resolved config, or find and edit a config file"
complete -c strument -n __fish_use_subcommand -a model-config -d "Print a model() block from a provider's catalog"
complete -c strument -n __fish_use_subcommand -a project -d "Inspect the recorded projects, or adopt a renamed one's history"
complete -c strument -n __fish_use_subcommand -a session -d "List, rename or delete this project's conversations"
complete -c strument -n __fish_use_subcommand -a tool -d "Run one observation tool and print what a model would see"
complete -c strument -n __fish_use_subcommand -a shell -d "Generate shell completions"

# Chat: the default command, so its flags stand until another command is named.
# Its positional arguments are files to pin, which is the one place the bare
# command line wants file completion.
set -l chat_cmd "not __fish_seen_subcommand_from $subcommands"
complete -c strument -n $chat_cmd -F
complete -c strument -n $chat_cmd -s m -l message -d "Send one message, apply the edits, and exit" -x
complete -c strument -n $chat_cmd -l session -d "Conversation to work in, created if new" -x
complete -c strument -n $chat_cmd -s c -l continue -d "Resume this session: restore its conversation from the record"
complete -c strument -n $chat_cmd -s M -l model -d "Model alias to use" -x -a "(__strument_models)"
complete -c strument -n $chat_cmd -l no-git -d "Disable git integration even inside a repository"
complete -c strument -n $chat_cmd -l no-color -d "Disable ANSI color and styling"
complete -c strument -n $chat_cmd -l dark-mode -d "Use colors suited to a dark terminal background"
complete -c strument -n $chat_cmd -l light-mode -d "Use colors suited to a light terminal background"
complete -c strument -n $chat_cmd -l no-auto-commits -d "Keep git integration but do not auto-commit edits"
complete -c strument -n $chat_cmd -l no-history -d "Do not write the session to the chat-history file"
complete -c strument -n $chat_cmd -l dry-run -d "Report edits without writing files or committing"
complete -c strument -n $chat_cmd -l no-shell -d "Disable the model's bash tool"
# --yes takes a prompt name here, unlike the bare --yes of trust and project
# adopt. It repeats and accepts comma-separated lists, which no completion can
# usefully offer past the first name.
complete -c strument -n $chat_cmd -l yes -d "Approve prompts of this type automatically" -x \
    -a "bash webfetch websearch steps context add-output all"
complete -c strument -n $chat_cmd -l consult-scope -d "Session context to include in /consult requests" -x -a "none files chat"
complete -c strument -n $chat_cmd -l version -d "Print version and exit"

# trust: a project directory, and the flag that skips its question.
complete -c strument -n "__fish_seen_subcommand_from trust" -a "(__fish_complete_directories)"
complete -c strument -n "__fish_seen_subcommand_from trust" -s y -l yes -d "Do not ask; for scripts"

# history.
complete -c strument -n "__fish_seen_subcommand_from history; and not __fish_seen_subcommand_from path edit markdown strip" \
    -a path -d "Print the path to this session's record"
complete -c strument -n "__fish_seen_subcommand_from history; and not __fish_seen_subcommand_from path edit markdown strip" \
    -a edit -d "Open it in \$VISUAL, \$EDITOR, or your platform's default editor"
complete -c strument -n "__fish_seen_subcommand_from history; and not __fish_seen_subcommand_from path edit markdown strip" \
    -a markdown -d "Print this session's history as markdown"
complete -c strument -n "__fish_seen_subcommand_from history; and not __fish_seen_subcommand_from path edit markdown strip" \
    -a strip -d "Remove stored tool payloads that nothing recent points at"
complete -c strument -n "__fish_seen_subcommand_from history; and __fish_seen_subcommand_from markdown" \
    -s t -l turns -d "Show only the last <n> turns" -x
complete -c strument -n "__fish_seen_subcommand_from history; and __fish_seen_subcommand_from strip" \
    -l older-than -d "Strip payloads nothing has referenced in this long (default: 90d)" -x
complete -c strument -n "__fish_seen_subcommand_from history; and __fish_seen_subcommand_from strip" \
    -s y -l yes -d "Strip without asking"

# session.
complete -c strument -n "__fish_seen_subcommand_from session; and not __fish_seen_subcommand_from list rename delete" \
    -a list -d "List this project's sessions"
complete -c strument -n "__fish_seen_subcommand_from session; and not __fish_seen_subcommand_from list rename delete" \
    -a rename -d "Rename a session, keeping its record"
complete -c strument -n "__fish_seen_subcommand_from session; and not __fish_seen_subcommand_from list rename delete" \
    -a delete -d "Delete a session and everything recorded in it"
complete -c strument -n "__fish_seen_subcommand_from session; and __fish_seen_subcommand_from delete" \
    -s y -l yes -d "Delete without asking"

# config. The scope flags name one file, so they belong to path and edit; models
# and default print the merge of both and refuse them.
complete -c strument -n "__fish_seen_subcommand_from config; and not __fish_seen_subcommand_from models default path edit" \
    -a models -d "Print the config's model aliases, one per line"
complete -c strument -n "__fish_seen_subcommand_from config; and not __fish_seen_subcommand_from models default path edit" \
    -a default -d "Print the config's default model alias"
complete -c strument -n "__fish_seen_subcommand_from config; and not __fish_seen_subcommand_from models default path edit" \
    -a path -d "Print the path to a config file, whether or not it exists"
complete -c strument -n "__fish_seen_subcommand_from config; and not __fish_seen_subcommand_from models default path edit" \
    -a edit -d "Open a config file in \$VISUAL, \$EDITOR, or your platform's default editor"
complete -c strument -n "__fish_seen_subcommand_from config; and __fish_seen_subcommand_from path edit markdown strip" \
    -l user -d "Act on the user config (the default)"
complete -c strument -n "__fish_seen_subcommand_from config; and __fish_seen_subcommand_from path edit markdown strip" \
    -l project -d "Act on this project's config"

# model-config. Its positional is a provider's model slug, which nothing here
# can enumerate without a network round trip.
complete -c strument -n "__fish_seen_subcommand_from model-config" -s s -l source -d "Metadata source" -x -a openrouter
complete -c strument -n "__fish_seen_subcommand_from model-config" -l provider-name -d "Provider variable name in the generated block" -x
complete -c strument -n "__fish_seen_subcommand_from model-config" -l proxy -d "SOCKS5 proxy for the catalog fetch" -x

# project.
complete -c strument -n "__fish_seen_subcommand_from project; and not __fish_seen_subcommand_from list adopt ignore" \
    -a list -d "List the recorded projects and their state directories"
complete -c strument -n "__fish_seen_subcommand_from project; and not __fish_seen_subcommand_from list adopt ignore" \
    -a adopt -d "Merge a renamed project's recorded history into this one"
complete -c strument -n "__fish_seen_subcommand_from project; and not __fish_seen_subcommand_from list adopt ignore" \
    -a ignore -d "Stop offering a renamed project's history at startup"
complete -c strument -n "__fish_seen_subcommand_from project; and __fish_seen_subcommand_from list" \
    -s a -l all -d "Include projects whose directory still exists"
complete -c strument -n "__fish_seen_subcommand_from project; and __fish_seen_subcommand_from adopt" \
    -s y -l yes -d "Do not ask; for scripts"
# adopt and ignore both name a project's old path or its state directory.
complete -c strument -n "__fish_seen_subcommand_from project; and __fish_seen_subcommand_from adopt ignore" \
    -a "(__fish_complete_directories)"

# tool.
complete -c strument -n "__fish_seen_subcommand_from tool; and not __fish_seen_subcommand_from read grep glob ls symbol" \
    -a read -d "Read a window of a file"
complete -c strument -n "__fish_seen_subcommand_from tool; and not __fish_seen_subcommand_from read grep glob ls symbol" \
    -a grep -d "Search file contents"
complete -c strument -n "__fish_seen_subcommand_from tool; and not __fish_seen_subcommand_from read grep glob ls symbol" \
    -a glob -d "Match files by path pattern"
complete -c strument -n "__fish_seen_subcommand_from tool; and not __fish_seen_subcommand_from read grep glob ls symbol" \
    -a ls -d "List a directory"
complete -c strument -n "__fish_seen_subcommand_from tool; and not __fish_seen_subcommand_from read grep glob ls symbol" \
    -a symbol -d "Look a name up in the language parser"
complete -c strument -n "__fish_seen_subcommand_from tool" -l root -d "Project root" -r -a "(__fish_complete_directories)"
complete -c strument -n "__fish_seen_subcommand_from tool" -l json -d "Print the call and its result as JSON"
complete -c strument -n "__fish_seen_subcommand_from tool; and __fish_seen_subcommand_from read" -F
complete -c strument -n "__fish_seen_subcommand_from tool; and __fish_seen_subcommand_from read" -l offset -d "First line to return (1-based)" -x
complete -c strument -n "__fish_seen_subcommand_from tool; and __fish_seen_subcommand_from read" -l limit -d "How many lines to return" -x
complete -c strument -n "__fish_seen_subcommand_from tool; and __fish_seen_subcommand_from ls" -a "(__fish_complete_directories)"
complete -c strument -n "__fish_seen_subcommand_from tool; and __fish_seen_subcommand_from grep" -l glob -d "Only search paths matching this glob" -x
complete -c strument -n "__fish_seen_subcommand_from tool; and __fish_seen_subcommand_from grep" -l path -d "Only search under this directory" -r -a "(__fish_complete_directories)"
complete -c strument -n "__fish_seen_subcommand_from tool; and __fish_seen_subcommand_from grep" -l mode -d "What to return" -x -a "files content count"
complete -c strument -n "__fish_seen_subcommand_from tool; and __fish_seen_subcommand_from grep" -l ignore-case -d "Match case-insensitively"
complete -c strument -n "__fish_seen_subcommand_from tool; and __fish_seen_subcommand_from grep" -l context-lines -d "Lines either side of each match" -x
complete -c strument -n "__fish_seen_subcommand_from tool; and __fish_seen_subcommand_from symbol" -l kind -d "Where the name is declared, or where it is used" -x -a "definition reference"

# shell.
complete -c strument -n "__fish_seen_subcommand_from shell" -a "bash fish"
