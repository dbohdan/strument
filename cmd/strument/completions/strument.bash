# Bash completions for strument.
#
# Verified by driving _strument_complete directly, not by reading; see
# cmd/strument/completions_test.go, which also holds the names below to the
# ones kong actually parses, in both directions. That check was one-directional
# once, and three names that no longer existed — a `version` command, a
# `--yes-shell` flag, a `-r` short for `tool --root` — survived here for months.

_strument_commands="chat trust history config model-config project session tool shell usage"
_strument_chat_options="-m --message -s --session -c --continue -M --model --no-git --no-color --dark-mode --light-mode --no-auto-commits --no-history --dry-run --no-shell --yes --consult-scope --version"
_strument_yes_names="bash webfetch websearch steps context add-output all"
_strument_trust_options="-y --yes"
_strument_history_commands="path edit markdown strip"
_strument_history_strip_options="--older-than -y --yes"
_strument_history_options="-s --session"
_strument_session_commands="list rename delete"
_strument_history_markdown_options="-t --turns"
_strument_config_commands="models default path edit"
_strument_config_options="--user --project"
_strument_model_config_options="-s --source --provider-name --proxy"
_strument_project_commands="list adopt ignore"
_strument_project_list_options="-a --all"
_strument_project_adopt_options="-y --yes"
_strument_tool_commands="read grep glob ls symbol run_code"
_strument_tool_options="--root --json"

# Every option that takes a value, so the scanner does not read one as a
# subcommand: `strument -M trust` names a model, not the trust command.
_strument_value_options="-m --message -M --model --yes --consult-scope -s --source --provider-name --proxy --root --offset --limit --glob --path --mode --context-lines --kind"

_strument_find_models() {
    command -v strument >/dev/null 2>&1 && strument config models 2>/dev/null
}

_strument_words() {
    COMPREPLY=($(compgen -W "$1" -- "$cur"))
}

_strument_complete() {
    local cur prev command sub word i expecting
    cur="${COMP_WORDS[COMP_CWORD]}"
    prev="${COMP_WORDS[COMP_CWORD-1]}"
    command=""
    sub=""
    expecting=0

    # First pass: which command and subcommand are we inside? Options that take
    # a value consume the next word, so it is never mistaken for a command.
    for ((i = 1; i < COMP_CWORD; i++)); do
        word="${COMP_WORDS[i]}"
        if ((expecting)); then
            expecting=0
            continue
        fi
        case " $_strument_value_options " in
        *" $word "*)
            expecting=1
            continue
            ;;
        esac
        case "$word" in
        -*) continue ;;
        esac
        if [[ -z $command ]]; then
            case " $_strument_commands " in
            *" $word "*) command=$word ;;
            *) break ;; # a file argument to chat; nothing after it is a command
            esac
        elif [[ -z $sub ]]; then
            sub=$word
        fi
    done

    # A value for the option just typed, whichever command we are in.
    case "$prev" in
    -M | --model)
        _strument_words "$(_strument_find_models)"
        return
        ;;
    --yes)
        # Only chat's --yes takes a value; trust's and project adopt's are
        # booleans, and offering prompt names after those would be a lie.
        if [[ -z $command || $command == chat ]]; then
            _strument_words "$_strument_yes_names"
        fi
        return
        ;;
    --consult-scope) _strument_words "none files chat" ; return ;;
    --mode) _strument_words "files content count" ; return ;;
    --kind) _strument_words "definition reference" ; return ;;
    -s | --source) _strument_words openrouter ; return ;;
    --root | --path)
        compopt -o dirnames
        return
        ;;
    esac
    case " $_strument_value_options " in
    # A value we cannot enumerate: a message, a glob, a line count. Offer
    # nothing rather than the command list, which is what the old script did.
    *" $prev "*) return ;;
    esac

    if [[ $cur == --model=* ]]; then
        COMPREPLY=($(compgen -W "$(_strument_find_models)" -- "${cur#--model=}"))
        COMPREPLY=("${COMPREPLY[@]/#/--model=}")
        return
    fi

    case "$command" in
    "" | chat)
        # The default command. Its positional arguments are files to pin, so
        # offer them alongside the command names.
        _strument_words "$_strument_commands $_strument_chat_options"
        [[ $cur == -* ]] || compopt -o default
        ;;
    trust)
        if [[ $cur == -* ]]; then
            _strument_words "$_strument_trust_options"
        else
            compopt -o dirnames
        fi
        ;;
    history)
        # Only while no subcommand has been chosen: `history path` takes
        # nothing further, and offering its siblings there would suggest they
        # compose. markdown is the exception — it takes a turn count.
        if [[ $cur == -* && $sub == markdown ]]; then
            _strument_words "$_strument_history_markdown_options"
        elif [[ $cur == -* && $sub == strip ]]; then
            _strument_words "$_strument_history_strip_options"
        elif [[ $cur == -* ]]; then
            _strument_words "$_strument_history_options"
        elif [[ -z $sub ]]; then
            _strument_words "$_strument_history_commands"
        fi
        ;;
    session)
        # Only while no subcommand has been chosen: the arguments after one are
        # session names, which this cannot know.
        [[ -n $sub ]] || _strument_words "$_strument_session_commands"
        ;;
    config)
        if [[ $cur == -* ]]; then
            # The scope flags name one file, so they belong to path and edit;
            # models and default print the merge of both and refuse them.
            case "$sub" in
            path | edit) _strument_words "$_strument_config_options" ;;
            esac
        elif [[ -z $sub ]]; then
            _strument_words "$_strument_config_commands"
        fi
        ;;
    model-config) _strument_words "$_strument_model_config_options" ;;
    project)
        case "$sub" in
        list) _strument_words "$_strument_project_list_options" ;;
        adopt)
            if [[ $cur == -* ]]; then
                _strument_words "$_strument_project_adopt_options"
            else
                compopt -o dirnames
            fi
            ;;
        ignore) compopt -o dirnames ;;
        *) _strument_words "$_strument_project_commands" ;;
        esac
        ;;
    tool)
        case "$sub" in
        read)
            if [[ $cur == -* ]]; then
                _strument_words "$_strument_tool_options --offset --limit"
            else
                compopt -o default
            fi
            ;;
        grep) _strument_words "$_strument_tool_options --glob --path --mode --ignore-case --context-lines" ;;
        symbol) _strument_words "$_strument_tool_options --kind" ;;
        ls)
            if [[ $cur == -* ]]; then
                _strument_words "$_strument_tool_options"
            else
                compopt -o dirnames
            fi
            ;;
        glob) _strument_words "$_strument_tool_options" ;;
        run_code)
            if [[ $cur == -* ]]; then
                _strument_words "$_strument_tool_options"
            else
                compopt -o default
            fi
            ;;
        *) _strument_words "$_strument_tool_commands $_strument_tool_options" ;;
        esac
        ;;
    shell) _strument_words "bash fish" ;;
    usage)
        # The argument is a provider name or all; the names live in the state
        # directory, which completion cannot enumerate, so only the keyword.
        [[ $cur == -* ]] || _strument_words "all"
        ;;
    esac
}

complete -F _strument_complete strument
