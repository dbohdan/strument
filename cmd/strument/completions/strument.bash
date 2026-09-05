# Bash completions for strument.

_strument_commands="trust history config model-config tool shell version"
_strument_chat_options="-m --message -c --continue -M --model --no-git --no-color --dark-mode --light-mode --no-auto-commits --no-history --jsonl --dry-run --no-shell --yes --yes-shell --version"
_strument_config_commands="models default"
_strument_tool_commands="read grep glob ls symbol"
_strument_model_config_options="-s --source --provider-name --proxy"
_strument_tool_options="-r --root --json"

_strument_find_models() {
    command -v strument >/dev/null 2>&1 && strument config models 2>/dev/null
}

_strument_complete_models() {
    COMPREPLY=($(compgen -W "$(_strument_find_models)" -- "$cur"))
}

_strument_complete() {
    local cur prev command word expecting_value i tool_command
    cur="${COMP_WORDS[COMP_CWORD]}"
    prev="${COMP_WORDS[COMP_CWORD-1]}"
    command=""
    tool_command=""
    expecting_value=0

    if [[ $prev == -M || $prev == --model ]]; then
        _strument_complete_models
        return
    fi
    if [[ $cur == --model=* ]]; then
        local model_prefix=${cur#--model=}
        COMPREPLY=($(compgen -W "$(_strument_find_models)" -- "$model_prefix"))
        COMPREPLY=("${COMPREPLY[@]/#/--model=}")
        return
    fi

    for ((i = 1; i < COMP_CWORD; i++)); do
        word="${COMP_WORDS[i]}"
        if ((expecting_value)); then
            expecting_value=0
            continue
        fi
        case "$word" in
        -M|--model|-m|--message|--jsonl|--provider-name|--proxy|--root|--offset|--limit|--glob|--path|--mode|--kind)
            expecting_value=1
            ;;
        -s)
            [[ $command == model-config ]] && expecting_value=1
            ;;
        -r)
            [[ $command == tool ]] && expecting_value=1
            ;;
        trust|history|config|model-config|tool|shell|version)
            [[ -z $command ]] && command=$word
            ;;
        read|grep|glob|ls|symbol)
            [[ $command == tool && -z $tool_command ]] && tool_command=$word
            ;;
        esac
    done

    case "$command" in
    config)
        COMPREPLY=($(compgen -W "$_strument_config_commands" -- "$cur"))
        ;;
    model-config)
        if [[ $prev == --source || $prev == -s ]]; then
            COMPREPLY=($(compgen -W "openrouter" -- "$cur"))
        elif [[ $prev == --provider-name || $prev == --proxy ]]; then
            COMPREPLY=()
        else
            COMPREPLY=($(compgen -W "$_strument_model_config_options" -- "$cur"))
        fi
        ;;
    tool)
        if [[ $prev == --mode ]]; then
            COMPREPLY=($(compgen -W "files content count" -- "$cur"))
        elif [[ $prev == --kind ]]; then
            COMPREPLY=($(compgen -W "definition reference" -- "$cur"))
        elif [[ $prev == --root || $prev == --offset || $prev == --limit || $prev == --glob || $prev == --path ]]; then
            COMPREPLY=()
        elif [[ -n $tool_command ]]; then
            case "$tool_command" in
            grep)
                COMPREPLY=($(compgen -W "--glob --path --mode --ignore-case --context-lines" -- "$cur"))
                ;;
            symbol)
                COMPREPLY=($(compgen -W "--kind" -- "$cur"))
                ;;
            *)
                COMPREPLY=()
                ;;
            esac
        else
            COMPREPLY=($(compgen -W "$_strument_tool_commands $_strument_tool_options" -- "$cur"))
        fi
        ;;
    shell)
        COMPREPLY=($(compgen -W "bash fish" -- "$cur"))
        ;;
    trust|history|version)
        COMPREPLY=()
        ;;
    *)
        if [[ $prev == --offset || $prev == --limit || $prev == --max-count ]]; then
            COMPREPLY=()
        else
            COMPREPLY=($(compgen -W "$_strument_commands $_strument_chat_options" -- "$cur"))
        fi
        ;;
    esac
}

complete -F _strument_complete strument
