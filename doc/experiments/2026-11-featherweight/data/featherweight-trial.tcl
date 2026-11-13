#! /usr/bin/env tclsh
# Featherweight -- a small agent harness in a single file.
# Copyright (c) 2026 D. Bohdan
# License: MIT, https://dbohdan.com/mit-license/2026
#
# Developed by D. Bohdan and language models:
# GPT-6 Luna, DeepSeek V4.1 Flash 1017, Kimi K3.1 Pro, MiMo-V2.6-Flash,
# MiMo-V2.6-Pro, and GPT-6.1 Terra.
#
# TRIAL FORK. This copy is Featherweight at commit 915b3b3
# (https://github.com/dbohdan/featherweight), patched for one experiment in
# Strument's repository, doc/experiments/2026-11-featherweight. Three changes,
# each marked "Trial fork:" where it is made:
#
#   1. `debug turn --message TEXT` runs one live turn without Tk, through the
#      same request_async/apply_message loop the window uses.
#   2. A `provider_only` configuration key adds OpenRouter's provider.only
#      routing (no fallbacks) to each request, so the trial can pin providers
#      for this harness as it does for Strument.
#   3. During a headless turn the debug log goes to the state directory, as
#      it would go to the window, not to featherweight-debug.log in the
#      project the model is working on.
#
# Nothing else is changed: the prompt, the tools, folding and the loop are the
# upstream code. This fork is not Featherweight's development line.
#
# Featherweight is an application that presents a chat window where you talk to
# a language model. The application stores the conversation log, determines
# how to fit the most recent messages in the model's context window, sends the
# requests, and runs whatever tools the model asks for.
#
# The core loop is as follows:
#
# 1. The user message is added to the session.
# 2. The session, folded ("compacted") to fit, goes out to the model as JSON.
# 3. The answer optionally names tools; each tool runs; its result is appended.
# 4. Step 3 repeats until an answer without tool calls is received.
#
# This source file is organized in sections:
#
#   state      The data required to operate the harness: the configuration,
#              the session, the windows.
#   record     Working with the sessions on disk: append-only files of messages
#              and fold markers that are readable as a Tcl list.
#   size       Measuring requests for context fit and cost.
#   wire       The JSON a request is made of.
#   tools      The tools the model calls: file operations, Tcl eval, history
#              recall.
#   turn       Request-response pairs, tool calls.
#   interface  The Tk GUI, its debug log, and its commands.
#   script     The harness features with no GUI window, for debugging.
#   entry      The CLI and the command that starts the application.
#
# Without arguments Featherweight starts with the Tk user interface.
# Script mode exists for debugging:
# $ tclsh featherweight.tcl debug --help

package require Tcl 8.6 9

package require fileutil
package require json
package require json::write
package require sha256


namespace eval ::featherweight {
    variable version 0.1.0

    # Default configuration and state.
    variable config [dict create \
        debug true \
        \
        api_key_env_var OPENROUTER_API_KEY \
        endpoint https://openrouter.ai/api/v1/chat/completions \
        model xiaomi/mimo-v2.6-flash \
        context_size 1048576 \
        retries 5 \
        timeout 300 \
        exec_timeout 60 \
        run_code_timeout 300 \
        font_size 10 \
        timestamp_format {%Y-%m-%d %H:%M:%S %z} \
        system_prompt {You are an expert software developer working with a user on their code.
The user will ask for changes to the code, or ask questions about it.
If a request is ambiguous, ask clarifying questions before making changes.

You have tools for reading and editing files, running Tcl code, and learning about this process.
Prefer read_file, write_file, and edit_file for file operations.
Use run_code for other operations in the user's environment.} \
        \
        current_session default \
    ]

    # The sessions this process has read from disk: name -> messages. A session
    # not in here has not been looked at yet; see load_session.
    variable sessions [dict create]

    # Where the state directory is when script mode has moved it: a replayed
    # turn runs against a throwaway copy so the real sessions stay untouched.
    # Empty means the ordinary launch-directory state.
    variable state_root {}

    variable tools_available [subst {
        run_code {
            -name run_code
            -description {Evaluate Tcl code in an interpreter of its own. Tcllib is available. You receive the return value plus whatever the code writes to stdout/stderr. State does not persist between calls, so use files for anything that must survive. Commands run through exec are killed when they run past a timeout (60s by default): pass -timeout to exec -- for example exec -timeout 5m make -- when you expect a long run. The call as a whole is stopped after 5m; ::run_code::deadline gives it more time. Read or change the harness's own state -- its configuration, its sessions, its interface -- with ::run_code::harness {script}, which returns the script's result.}
            -param {code string {The code to evaluate.}}
            -required code
            -command ::featherweight::tool_run_code
        }
        read_file {
            -name read_file
            -description {Read a text file's contents with line numbers. Returns a window of the file; use offset and limit to page through a long one.}
            -param {path string {The file's path, relative to the project root. An absolute path also works; relative is preferred for project files.}}
            -param {offset integer {The first line to return, 1-based. Omit to start at the beginning.}}
            -param {limit integer {How many lines to return. Omit for a default window of 2000 lines.}}
            -required path
            -command ::featherweight::tool_read_file
        }
        write_file {
            -name write_file
            -description {Write a file with the given full contents, creating it or completely overwriting it if it already exists. Applies immediately. To change part of an existing file use edit_file instead; use this only when you are providing the whole file.}
            -param {path string {The file's path, relative to the project root. An absolute path also works; relative is preferred for project files.}}
            -param {content string {The complete contents of the file.}}
            -required {path content}
            -command ::featherweight::tool_write_file
        }
        edit_file {
            -name edit_file
            -description {Replace an exact span of text in a file. The edit applies immediately. Make one call per change; call it several times to make several changes.}
            -param {path string {The file's path, relative to the project root. An absolute path also works; relative is preferred for project files.}}
            -param {old_string string {The exact existing text to replace, character for character, including all whitespace, comments, and docstrings. It must match exactly once: include enough surrounding lines to pick out the one place you mean, and make a separate call for each place if you mean several.}}
            -param {new_string string {The text to put in its place.}}
            -param {replace_all boolean {Replace every occurrence of old_string instead of requiring exactly one. Default false. Use it when the same text should change everywhere in the file.}}
            -required {path old_string new_string}
            -command ::featherweight::tool_edit_file
        }
        about {
            -name about
            -description {Report facts about this Featherweight process: its version and script, when the process started, the current date and time, the Tcl and Tk versions, the platform, and this session's model and project root. Use it instead of inferring any of these.}
            -command ::featherweight::tool_about
        }
        recall {
            -name recall
            -description {Search this session's record: every message of the session as it was said, including earlier turns that have been compacted, summarized or dropped from this conversation. Use it to find something said, read or worked out earlier that you can no longer see. Matches in messages still in this conversation are counted, not shown. Results are newest first; each hit has an id, which you can pass back to see that message whole.}
            -param {pattern string {An FTS5 full-text query, as SQLite's MATCH takes: words are ANDed, or finds either, "words in quotes" is a phrase, word* matches a prefix, -word excludes. A query that is not valid FTS5 is searched for as a phrase. Required unless id is given.}}
            -param {from string {Which messages to search: "conversation" (the default) is what the user and you said; "tools" is tool output and tool calls; "all" is both.}}
            -param {newest integer {How many matching messages to show, newest first. Default 10, at most 50.}}
            -param {id string {Optional. A hit's id, such as "t4.3", to see that message whole; lines matching pattern, if one is given, are marked.}}
            -param {session string {Which session's record to search: the current one by default; name another to search it instead.}}
            -command ::featherweight::tool_recall
        }
    }]
    variable process_started [clock seconds]
}


namespace eval ::featherweight {
    variable W {}
    variable json_tools {}
    variable busy 0
    variable done 0

    # How many times the current request has been sent again after a failure.
    variable retry_count 0

    # The color of each debug category. A fold marker's underline is drawn
    # in the color of the tag that sets it, so the marker's own tag carries
    # both the underline and its color -- set the color anywhere else and
    # Tk draws the line in the widget's foreground instead.
    variable debug_colors [dict create \
        error #ff6b6b \
        request #4fc3f7 \
        response #a5d6a7 \
        tool #ce93d8 \
    ]

# Collapsible debug entries: their number, and which of them are open.
    variable debug_entry 0
    array set debug_open {}

    # Images waiting to go out with the next user message: name and stored path.
    variable attachments {}
    variable request_channels
    array set request_channels {}

    # One run_code call at a time: its number, its thread, its watchdog
    # timer and deadline, the child processes it has running, and where its
    # answer lands. See run_code_call.
    variable run_code_serial 0
    variable run_code_token {}
    variable run_code_thread {}
    variable run_code_timer {}
    variable run_code_limit 0
    variable run_code_children
    array set run_code_children {}
    variable run_code_payload
    array set run_code_payload {}
    variable run_code_expired
    array set run_code_expired {}

    # What immutable messages serialize to and count as, kept so a request
    # need not work it out three times over. See memo.
    variable memo_values
    array set memo_values {}
    variable memo_used
    array set memo_used {}
    variable memo_clock 0
    variable memo_bytes 0

    # The debug entries whose body has not been built yet: a request or a
    # tool result can be a megabyte and is folded shut anyway. See
    # debug_toggle.
    variable debug_text
    array set debug_text {}

    # The folded reasoning blocks in the conversation: their number, and
    # which of them are open. See ui_thinking.
    variable thinking_serial 0
    variable thinking_open
    array set thinking_open {}
}


# The directory that holds the configuration and the sessions, relative to the
# project root (the launch directory) -- or wherever script mode has pointed
# it, for a turn replayed against a copy of the state.
proc ::featherweight::state_dir {} {
    variable state_root

    if {$state_root ne {}} {
        return $state_root
    }
    return [file join [pwd] featherweight]
}


proc ::featherweight::config_path {} {
    return [file join [state_dir] config]
}


proc ::featherweight::sessions_dir {} {
    return [file join [state_dir] sessions]
}


# The file that holds one session. A session name doubles as its file name, so
# a name that is not a plain file name is refused here rather than escaping the
# sessions directory.
proc ::featherweight::session_path {name} {
    if {$name eq {}
            || $name in {. ..}
            || [file tail $name] ne $name
            || [string first "\\" $name] >= 0} {
        error "invalid session name \"$name\": a session name must be a plain file name, without path separators"
    }
    return [file join [sessions_dir] $name]
}


# Whether the session has a file. An unusable name has no session.
proc ::featherweight::session_exists {name} {
    if {[catch {session_path $name} path]} {
        return 0
    }
    return [file exists $path]
}


# The names of every session that has a file, sorted.
proc ::featherweight::session_names {} {
    set directory [sessions_dir]
    if {![file isdirectory $directory]} {
        return {}
    }

    set names {}
    foreach path [glob -nocomplain -directory $directory *] {
        if {![file isfile $path]} {
            continue
        }
        set name [file tail $path]
        if {![catch {session_path $name}]} {
            lappend names $name
        }
    }
    return [lsort $names]
}


# Read the configuration, filling in what an older file does not have, and
# make sure the state it names exists: the directories, the current session.
proc ::featherweight::init_config {} {
    variable config

    set path [config_path]
    if {[file exists $path]} {
        if {[catch {::fileutil::cat -encoding utf-8 $path} loaded options]} {
            error "could not load [list $path]: $loaded" {} [dict get $options -errorinfo]
        }
        if {[catch {dict size $loaded}]} {
            error "configuration in [list $path] is not a Tcl dict"
        }
        set config $loaded
    }

    if {![dict exists $config context_size]} {
        dict set config context_size 1048576
    }
    set context_size [dict get $config context_size]
    if {![string is integer -strict $context_size]
            || ($context_size != 0 && $context_size < 4096)} {
        error {context_size must be 0 (off) or an integer of at least 4096 tokens}
    }

    # Used as clock format expects; a default for configurations written
    # before it existed.
    if {![dict exists $config timestamp_format]} {
        dict set config timestamp_format {%Y-%m-%d %H:%M:%S %z}
    }

    # How often to send a request again when the network or the server drops
    # it, and how long one request may take.
    if {![dict exists $config retries]} {
        dict set config retries 5
    }
    set retries [dict get $config retries]
    if {![string is integer -strict $retries] || $retries < 0} {
        error {retries must be an integer of at least 0}
    }
    if {![dict exists $config timeout]} {
        dict set config timeout 300
    }
    set timeout [dict get $config timeout]
    if {![string is integer -strict $timeout] || $timeout < 1} {
        error {timeout must be an integer of at least 1 second}
    }

    # How long one command the model runs may take before it is killed. In
    # seconds; the model can set it lower or higher for a single call.
    if {![dict exists $config exec_timeout]} {
        dict set config exec_timeout 60
    }
    set exec_timeout [dict get $config exec_timeout]
    if {![string is integer -strict $exec_timeout] || $exec_timeout < 1} {
        error {exec_timeout must be an integer of at least 1 second}
    }
    set ::run_code::timeout [expr {$exec_timeout * 1000}]

    # How long one run_code call may take in all, code and commands
    # together, before it is stopped. In seconds. The model can ask for more
    # time for a single call with ::run_code::deadline.
    if {![dict exists $config run_code_timeout]} {
        dict set config run_code_timeout 300
    }
    set run_code_timeout [dict get $config run_code_timeout]
    if {![string is integer -strict $run_code_timeout] || $run_code_timeout < 1} {
        error {run_code_timeout must be an integer of at least 1 second}
    }

    # The baseline font size, in points, for the conversation, the debug log,
    # the labels and the input. 10 is the size Tk gives its fonts here.
    if {![dict exists $config font_size]} {
        dict set config font_size 10
    }
    set font_size [dict get $config font_size]
    if {![string is integer -strict $font_size] || $font_size < 6} {
        error {font_size must be an integer of at least 6}
    }

    # The current session must name a session file; anything else starts over
    # at the default session.
    if {![dict exists $config current_session]
            || ![session_exists [dict get $config current_session]]} {
        dict set config current_session default
    }

    set system_message [dict create \
        role system \
        content [dict get $config system_prompt] \
        time [clock seconds] \
    ]
    if {[llength [current_messages]] == 0} {
        set_current_messages [list $system_message]
    }
    save_config
}


# Write the configuration. Sessions are not part of it: each one is written to
# its own file by save_session, so this file stays small however long the
# conversations grow.
proc ::featherweight::save_config {} {
    variable config

    set path [config_path]
    set text {}
    dict for {key value} $config {
        append text [list $key $value] \n
    }

    if {[catch {write_text_file $path $text} error]} {
        debug_log error "could not save [list $path]: $error"
    }
}


proc ::featherweight::get_env {name {default %UNSET%}} {
    if {![info exists ::env($name)]} {
        if {$default ne {%UNSET%}} {
            return $default
        }

        error "environment variable [list $name] unset"
    }

    return $::env($name)
}


proc ::featherweight::api_key {} {
    variable config

    return [get_env [dict get $config api_key_env_var]]
}


# --- session files ---
#
# A session file is a Tcl list of records, one per line where possible.
# Message content can contain newlines, so a record may span lines: the file
# stays readable as Tcl. Reads and writes parse the whole file as a list:
# parsing individual lines could fail on an unmatched brace. See
# session_records and join_session_records.
#
# Session files are append-only as the conversation grows, retaining even
# the history cut by compaction: the file is the conversation itself.
# Compaction appends a marker containing a summary; the live conversation
# becomes that summary and everything after the marker. Earlier history
# remains untouched, so a crash during compaction cannot lose it.

# Parse a session file's text into its records. Raises an error naming the
# record at fault rather than failing partway through.
proc ::featherweight::session_records {content} {
    if {[catch {llength $content} count]} {
        error "session is not a Tcl list"
    }
    set records {}
    for {set index 0} {$index < $count} {incr index} {
        set record [lindex $content $index]
        if {[catch {dict keys $record}]} {
            set shown [string range [list $record] 0 79]
            error "record $index is not a record dict: $shown"
        }
        lappend records $record
    }
    return $records
}


# Records as the text a session file holds: one record per line where the
# record allows it. The counterpart to session_records.
proc ::featherweight::join_session_records {records} {
    set text {}
    foreach record $records {
        append text [list $record] \n
    }
    return $text
}


# A message record: it has a role -- user, assistant, tool, or system.
proc ::featherweight::is_message {record} {
    if {[catch {dict keys $record}]} {
        return 0
    }
    return [expr {[dict exists $record role] && [dict get $record role] ne {}}]
}


# A marker record: a type and no role. A fold's marker carries the summary
# that stands in for what it cut.
proc ::featherweight::is_marker {record} {
    if {[catch {dict keys $record}]} {
        return 0
    }
    return [expr {![dict exists $record role]
        && [dict exists $record type] && [dict get $record type] ne {}}]
}


# The session's conversation: the last marker's summary followed by its
# messages. The summary contains the system prompt and a note replacing
# the dropped messages. A file with no markers, including one from before
# markers existed, contains the full conversation.
proc ::featherweight::session_messages {records} {
    set live {}
    foreach record $records {
        if {[is_marker $record]} {
            if {[dict exists $record summary]} {
                set live [dict get $record summary]
            } else {
                set live {}
            }
            continue
        }
        if {![is_message $record]} {
            set shown [string range [list $record] 0 79]
            error "record is neither a message nor a marker: $shown"
        }
        lappend live $record
    }
    return $live
}


# One session as its file holds it: its last marker's summary and the
# messages after it. A session with no file yet has no messages; a file that
# cannot be read or understood is an error rather than an empty session, so
# that a later save never overwrites it.
proc ::featherweight::read_session {name} {
    set path [session_path $name]
    if {![file exists $path]} {
        return {}
    }
    if {[catch {::fileutil::cat -encoding utf-8 $path} content options]} {
        error "could not load [list $path]: $content" {} [dict get $options -errorinfo]
    }
    if {[catch {session_records $content} records]} {
        error "session in [list $path]: $records"
    }
    if {[catch {session_messages $records} messages]} {
        error "session in [list $path]: $messages"
    }
    return $messages
}


# Save a session. If its messages extend the file's conversation, append
# the difference; otherwise, rewrite the file. The app does not normally
# change earlier messages. A session not read by this process is left alone.
proc ::featherweight::save_session {name} {
    variable sessions

    if {![dict exists $sessions $name]} {
        return
    }

    set path [session_path $name]
    file mkdir [file dirname $path]
    set records [dict get $sessions $name]

    # What the file already holds as a conversation. A fold leaves the
    # conversation it cut away above the marker, so the file's raw records
    # are more than the conversation it has now; compare against that.
    set existing {}
    if {[file exists $path]} {
        if {[catch {
            set existing [session_messages \
                [session_records [::fileutil::cat -encoding utf-8 $path]]]
        } error]} {
            debug_log error "could not save [list $path]: $error"
            return
        }
    }

    # Append an extension; rewrite only if earlier messages changed.
    if {[llength $records] >= [llength $existing]
            && [lrange $records 0 [expr {[llength $existing] - 1}]] eq $existing} {
        append_session_records $name [lrange $records [llength $existing] end]
        return
    }

    if {[catch {
        write_text_file $path [join_session_records $records]
    } error]} {
        debug_log error "could not save [list $path]: $error"
    }
}


# Append records to a session file. The app rewrites a file only when
# save_session receives a set that changes earlier messages.
proc ::featherweight::append_session_records {name records} {
    if {$records eq {}} {
        return 1
    }
    set path [session_path $name]
    file mkdir [file dirname $path]
    if {[catch {
        set channel [open $path a]
        fconfigure $channel -encoding utf-8 -translation lf
        puts -nonewline $channel [join_session_records $records]
        close $channel
    } error]} {
        catch {close $channel}
        debug_log error "could not save [list $path]: $error"
        return 0
    }
    return 1
}


# The session's messages, read from its file the first time this process needs
# them and kept in memory after that.
proc ::featherweight::load_session {name} {
    variable sessions

    if {![dict exists $sessions $name]} {
        dict set sessions $name [read_session $name]
        # Warm the serialization cache while the interface is idle, so the
        # first request of a session is not the one that pays for it: on the
        # 790-message conversation that first serialization is 85ms. See
        # memo.
        set messages [dict get $sessions $name]
        after idle [list ::featherweight::memo_warm $messages]
    }
    return [dict get $sessions $name]
}


proc ::featherweight::current_session_name {} {
    variable config
    return [dict get $config current_session]
}


proc ::featherweight::current_messages {} {
    return [load_session [current_session_name]]
}


# Set the session's current messages. For a fold, append a marker carrying
# the summary, followed by the retained messages. Earlier history remains
# intact above the marker but is ignored in the live conversation.
proc ::featherweight::set_current_messages {messages {folded {}}} {
    variable config
    variable sessions

    set name [dict get $config current_session]
    dict set sessions $name $messages

    if {$folded eq {}} {
        save_session $name
        return
    }

    # The summary is what stands in for what was cut: the system prompt the
    # fold keeps and the note that replaces the dropped messages. What
    # follows the marker is what the conversation keeps from there on.
    set summary [dict get $folded summary]
    set tail [lrange $messages [llength $summary] end]
    set marker [dict merge [dict create type fold cut [llength $summary]] $folded]
    append_session_records $name [concat [list $marker] $tail]
}


# Store one message in the current session and give it a Unix time if it does
# not have one: a user turn, an assistant reply, a tool result. Messages written
# before timestamps existed keep none rather than being given a false one.
# Returns the message as stored, time included, for the caller to display.
proc ::featherweight::append_message message {
    variable config
    variable sessions

    if {![dict exists $message time]} {
        dict set message time [clock seconds]
    }

    set name [dict get $config current_session]
    load_session $name
    dict lappend sessions $name $message

    append_session_records $name [list $message]

    return $message
}


# Approximate tokens the same way Strument's RuneCounter does: Unicode code
# points divided by four. Count the wire representation so tool arguments and
# per-message overhead are not invisible to the context budget.
# --- counting what a request costs ---
#
# A request is a budget: the model has a context window, and every message,
# tool schema and image spends from it. These are approximate counts -- code
# points over four -- good enough to decide what fits and what does not.

proc ::featherweight::estimate_tokens {text} {
    return [expr {[string length $text] / 4}]
}


proc ::featherweight::messages_tokens messages {
    set total 0
    foreach message $messages {
        incr total [message_tokens $message]
    }
    return $total
}


# A message's share of the context budget. An image is estimated from its
# dimensions, not counted as its bytes: see image_tokens.
proc ::featherweight::message_tokens message {
    return [memo tokens $message {message_tokens_uncached $message}]
}


# Where message_tokens' count is actually worked out, before it is kept.
proc ::featherweight::message_tokens_uncached message {
    if {[dict exists $message content] && [is_parts [dict get $message content]]} {
        set total 0
        foreach part [dict get $message content] {
            if {[dict get $part type] eq {image}} {
                incr total [image_tokens [dict get $part path]]
            } else {
                incr total [estimate_tokens [dict get $part text]]
            }
        }
        return $total
    }
    return [estimate_tokens [json_message $message]]
}


# What a pure function of an immutable message returned, kept so the next
# call does not work it out again. memo name key body runs body in the
# caller's scope and keeps what it returns under name and key; the next call
# with the same name and key finds it and body does not run again. A request
# serializes the conversation three times -- fold_plan and prepare_request
# each count tokens over it, which serializes every message to measure it,
# and json_payload serializes them all again to send -- and a message does
# not change once appended, so only the first of those passes does any work.
# On the 750-message conversation this takes those passes from about 200ms
# to one.
#
# The cache is bounded twice over, by entries and by bytes, and drops the
# least recently used fifth in one pass when it grows past either bound.
proc ::featherweight::memo {name key body} {
    variable memo_bytes
    variable memo_clock
    variable memo_used
    variable memo_values

    set id $name,$key
    if {[info exists memo_values($id)]} {
        set memo_used($id) [incr memo_clock]
        return $memo_values($id)
    }
    set value [uplevel 1 $body]
    set memo_values($id) $value
    set memo_used($id) [incr memo_clock]
    incr memo_bytes [string length $value]
    if {[array size memo_values] > 4000 || $memo_bytes > 32000000} {
        set victims {}
        foreach victim [array names memo_used] {
            lappend victims [list $memo_used($victim) $victim]
        }
        set victims [lsort -integer -index 0 $victims]
        foreach victim [lrange $victims 0 [expr {[llength $victims] / 5 - 1}]] {
            set id [lindex $victim 1]
            incr memo_bytes [expr {-[string length $memo_values($id)]}]
            unset memo_values($id) memo_used($id)
        }
    }
    return $value
}


# What every message counts as, worked out now so the next request finds it
# in the cache. See memo.
proc ::featherweight::memo_warm messages {
    foreach message $messages {
        catch {message_tokens $message}
    }
}


# The width and height of an image file, read from its header. An empty list
# means a format this does not parse, which leaves the estimate on its high
# default.
proc ::featherweight::image_dimensions {path} {
    if {![file isfile $path]} {
        return {}
    }
    set channel [open $path r]
    fconfigure $channel -translation binary

    # PNG: the dimensions are in the first chunk, big-endian.
    set header [read $channel 24]
    if {[string equal -length 8 $header "\x89PNG\r\n\x1a\n"]} {
        close $channel
        binary scan [string range $header 16 23] II width height
        return [list $width $height]
    }

    # GIF: the logical screen descriptor, little-endian.
    if {[string equal -length 3 $header "GIF"]} {
        close $channel
        binary scan [string range $header 6 9] ss width height
        return [list $width $height]
    }

    # JPEG: walk the segments to a start-of-frame marker. The dimensions come
    # after the precision byte, big-endian.
    if {[string equal -length 2 $header "\xff\xd8"]} {
        seek $channel 2 start
        while {1} {
            set byte [read $channel 1]
            if {$byte eq {} || [scan $byte %c] != 0xFF} {
                break
            }
            set marker [read $channel 1]
            if {$marker eq {}} {
                break
            }
            scan $marker %c code
            if {$code == 0xD8 || $code == 0x01 || ($code >= 0xD0 && $code <= 0xD7)} {
                continue
            }
            set length [read $channel 2]
            if {[string length $length] < 2} {
                break
            }
            binary scan $length S size
            if {$code == 0xC0 || $code == 0xC1 || $code == 0xC2 || $code == 0xC3
                    || $code == 0xC5 || $code == 0xC6 || $code == 0xC7
                    || $code == 0xC9 || $code == 0xCA || $code == 0xCB
                    || $code == 0xCD || $code == 0xCE || $code == 0xCF} {
                set frame [read $channel 5]
                close $channel
                binary scan [string range $frame 1 4] SS height width
                return [list $width $height]
            }
            seek $channel [expr {$size - 2}] current
        }
    }

    close $channel
    return {}
}


# What one image adds to a request.
#
# There is no counting an image the way there is counting text: the number is
# the provider's, computed from dimensions by a formula each one documents
# differently, so this is declared rather than measured. OpenAI bills a base
# plus one charge per 512-pixel tile, which is what an OpenRouter request
# meets; Anthropic, for comparison, documents width x height / 750. Both are
# approximations: a provider that resizes before counting will disagree.
# Treat the number as the right order of magnitude, which is what a context
# guard needs, and not as a bill.
proc ::featherweight::image_tokens {path} {
    set dimensions [image_dimensions $path]
    if {[llength $dimensions] != 2} {
        # Deliberately on the high side: a guard that under-counts lets a
        # request through that the provider then refuses, which is the
        # failure with a cost attached.
        return 1600
    }
    lassign $dimensions width height

    set wide [expr {($width + 511) / 512}]
    set tall [expr {($height + 511) / 512}]
    set tokens [expr {85 + 170 * $wide * $tall}]

    # Providers downscale very large images before counting, so the formula
    # runs away where they do not.
    if {$tokens > 2600} {
        set tokens 2600
    }
    return $tokens
}


proc ::featherweight::tools_tokens json_tools {
    return [estimate_tokens [::json::write array {*}$json_tools]]
}


# What the automatic fold aims at: half the context window, never less than
# 2048 tokens.
proc ::featherweight::history_budget {} {
    variable config
    if {![dict exists $config context_size]} {
        return 0
    }
    set context_size [dict get $config context_size]
    if {![string is integer -strict $context_size] || $context_size <= 0} {
        return 0
    }
    return [expr {max($context_size / 2, 2048)}]
}


# /context-size's answer: what the next request costs, and what the window
# is.
proc ::featherweight::context_report {} {
    variable config
    variable json_tools

    set size [dict get $config context_size]
    set history [messages_tokens [current_messages]]
    set tools [tools_tokens $json_tools]
    set staged [attachments_tokens]
    set total [expr {$history + $tools + $staged}]
    set report [format {Approximate request usage: %d tokens (%d history + %d tool schemas + %d staged attachments).} $total $history $tools $staged]
    if {$size > 0} {
        append report [format "\nContext window: %d tokens; automatic compaction at %d history tokens." $size [history_budget]]
        append report [format "\nEstimated total usage: %.1f%% of the window." [expr {100.0 * $total / $size}]]
    } else {
        append report "\nAutomatic compaction is disabled."
    }
    return $report
}


# --- folding history to fit ---
#
# A conversation outgrows its window. A fold drops the oldest turns and
# keeps what the user said in them, quoted and summarized, and records the
# cut in the session rather than making it.

# A note from the harness itself, shown to the model as a user message: the
# [featherweight] prefix marks it as ours rather than the person's.
proc ::featherweight::harness_note {text} {
    return [dict create \
        role user \
        content "\[featherweight\] $text" \
        time [clock seconds] \
    ]
}


# Whether a message is one of the harness's own notes.
proc ::featherweight::is_harness_note message {
    return [expr {[dict get $message role] eq {user}
        && [string match {\[featherweight\]*} [content_display [dict get $message content]]]}]
}


# What the user said, message by message and trimmed, for a fold to quote.
proc ::featherweight::user_texts messages {
    set texts {}
    foreach message $messages {
        if {[dict get $message role] eq {user} && ![is_harness_note $message]} {
            set text [string trim [content_display [dict get $message content]]]
            if {$text ne {}} {
                lappend texts $text
            }
        }
    }
    return $texts
}


# The note that stands in for the turns a fold drops: what the user said in
# them, quoted word for word, and where the rest of them went.
proc ::featherweight::roll_note texts {
    set text {Earlier turns of this session were dropped to keep it inside the context window. What the user said in them is quoted below, word for word, oldest first; the rest of those turns is in the session record.}
    foreach message $texts {
        append text "\n\n> [string map [list "\n" "\n> "] $message]"
    }
    # The pointer is in two places, as Strument's recall experiments settled:
    # a clause in the label read first, and the instruction after the quotes,
    # nearest the turn that needs it. Without it models answer "I don't know"
    # with the answer one search away.
    append text "\n\nThe rest of those turns, including your own replies and the output of every tool call, is kept in the session record, and the recall tool searches it. When a question concerns something said, read or worked out in those turns, search the record before deciding you don't know; file contents and command output need from=\"all\". Re-read a file only when its current contents are what you need."
    return [harness_note $text]
}


# Work out what a fold would do, without doing it. Returns a dict holding:
#   fold       1 when a fold would happen, 0 when it would not
#   why        why not: off, fits, no-turn, no-gain
#   before     the history's tokens now; after, what it would be left with
#   cut        the message the fold would start from (when it would fold)
#   note       the note that would stand in for the dropped messages
#   said       the user texts that note would quote
#   compacted  the messages the session would then hold
#   target     the token count the cut is chosen against
# Pure, but for the note's own timestamp: it reads the session and writes
# nothing, so /compact, debug fold and debug request can all ask the same
# question and get the same answer.
proc ::featherweight::fold_plan {{force 0}} {
    set messages [current_messages]
    set before [messages_tokens $messages]
    set budget [history_budget]
    if {!$force} {
        if {$budget == 0} {
            return [dict create fold 0 why off before $before]
        }
        if {$before <= $budget} {
            return [dict create fold 0 why fits before $before]
        }
    }

    # A forced fold aims at half of what is there rather than at half the
    # budget, and never higher than the automatic target -- it cannot leave
    # the session over a window the automatic rule would have respected.
    if {$force} {
        set target [expr {$before / 2}]
        if {$budget > 0} {
            set target [expr {min($target, $budget / 2)}]
        }
    } else {
        set target [expr {$budget / 2}]
    }

    # Keep the system prompt outside the fold. Every other genuine user message
    # begins a turn, so cutting there cannot orphan a tool call from its result.
    set starts {}
    for {set index 1} {$index < [llength $messages]} {incr index} {
        set message [lindex $messages $index]
        if {[dict get $message role] eq {user} && ![is_harness_note $message]} {
            lappend starts $index
        }
    }

    set said {}
    set cut -1
    set note {}
    foreach index $starts {
        set candidate_said [concat $said [user_texts [lrange $messages 1 $index-1]]]
        set candidate_note [roll_note $candidate_said]
        set tail [lrange $messages $index end]
        if {[messages_tokens [list $candidate_note {*}$tail]] <= $target} {
            set cut $index
            set said $candidate_said
            set note $candidate_note
            break
        }
    }
    if {$cut < 0 && [llength $starts] > 0} {
        # Even one turn may exceed the target. Keep the latest turn whole rather
        # than destroying its context or repeatedly folding on every request.
        set cut [lindex $starts end]
        set said [user_texts [lrange $messages 1 $cut-1]]
        set note [roll_note $said]
    }
    if {$cut < 0} {
        return [dict create fold 0 why no-turn before $before target $target]
    }

    set before_count [llength $messages]
    # The marker's summary contains the system prompt and the note
    # replacing the dropped messages. The retained tail follows the marker.
    set summary [list [lindex $messages 0] $note]
    set compacted [list [lindex $messages 0] $note {*}[lrange $messages $cut end]]
    set after [messages_tokens $compacted]
    if {$after >= $before} {
        return [dict create fold 0 why no-gain before $before after $after \
            cut $cut said $said note $note before_count $before_count \
            target $target compacted $compacted summary $summary]
    }
    return [dict create fold 1 why done before $before after $after cut $cut \
        said $said note $note before_count $before_count target $target \
        compacted $compacted summary $summary]
}


# Why a fold would not happen, in words. compact_context prefixes this with
# "context not folded: "; debug fold reports it on its own.
proc ::featherweight::fold_decline {plan} {
    switch -- [dict get $plan why] {
        off {
            return {automatic folding is off (context_size is 0)}
        }
        fits {
            return {the history already fits the context budget}
        }
        no-turn {
            return {the session has no user turn to cut at}
        }
        no-gain {
            return "keeping the latest turn whole and quoting the user's words verbatim would leave approximately [dict get $plan after] tokens, at least as many as the [dict get $plan before] it has now"
        }
        default {
            return [dict get $plan why]
        }
    }
}


# Fold the current session's history toward the context budget; called
# before every request, and by /compact. A fold only happens when it leaves
# the history smaller -- quoting the user's words is not free -- and $force
# folds even when the history already fits, reporting a fold it cannot do.
proc ::featherweight::compact_context {{force 0}} {
    set plan [fold_plan $force]
    if {![dict get $plan fold]} {
        if {$force} {
            debug_log tool "context not folded: [fold_decline $plan]"
        }
        return 0
    }

    set label {compacted history}
    if {$force} {
        set label {folded on request}
    }
    set cut [dict get $plan cut]
    set_current_messages [dict get $plan compacted] [dict create \
        label $label \
        summary [dict get $plan summary] \
    ]
    set before_count [dict get $plan before_count]
    debug_log tool "$label: dropped [expr {$cut - 1}] of [expr {$before_count - 1}] messages ([dict get $plan before] -> [dict get $plan after] tokens); user messages kept verbatim"
    refresh_session
    return 1
}


# What the next request carries: the conversation folded to fit, or an error
# when even that is too much for the window.
proc ::featherweight::prepare_request {} {
    variable config
    variable json_tools

    compact_context
    set size [dict get $config context_size]
    set history [messages_tokens [current_messages]]
    set tools [tools_tokens $json_tools]
    set total [expr {$history + $tools}]
    if {$size > 0 && $total > $size} {
        error "estimated request is $total tokens, over the configured context_size of $size; start a new session or increase /context-size"
    }
    return [current_messages]
}


# Is this message content a list of parts -- words and images -- rather than
# one string? The parts are dicts with a type.
# --- the wire format ---
#
# What a request is made of: the tool schemas, the messages as JSON, and the
# images as data URLs. A message carries what the API expects and nothing
# more; what is ours -- the time a message was made, the model's reasoning --
# is kept out here.

proc ::featherweight::is_parts value {
    if {[catch {llength $value}] || [llength $value] == 0} {
        return 0
    }
    set first [lindex $value 0]
    if {[catch {dict exists $first type}]} {
        return 0
    }
    return [dict exists $first type]
}


# The MIME type of an image, from its extension.
proc ::featherweight::image_mime name {
    switch -- [string tolower [file extension $name]] {
        .jpg - .jpeg { return image/jpeg }
        .gif { return image/gif }
        .webp { return image/webp }
        default { return image/png }
    }
}


# An image part as a data URL. An empty string means the file could not be
# read; the caller should report the missing attachment without failing
# the request or leaving the interface waiting.
proc ::featherweight::image_data_url part {
    set channel {}
    if {[catch {
        set channel [open [dict get $part path] r]
        fconfigure $channel -translation binary
        set data [read $channel]
        close $channel
    }]} {
        if {$channel ne {}} {
            catch {close $channel}
        }
        return {}
    }
    return "data:[image_mime [dict get $part name]];base64,[binary encode base64 $data]"
}


# A message's content as the API wants it: a list of parts becomes a content
# array where each image carries its file as a data URL. A plain string never
# reaches here; json_message sends it as it is.
proc ::featherweight::json_content parts {
    set out {}

    foreach part $parts {
        if {[dict get $part type] eq {image}} {
            set url [image_data_url $part]
            if {$url eq {}} {
                # The file is gone -- moved on disk, or deleted by a detach
                # before those stopped deleting. Say so rather than fail the
                # request: the words around it still go out.
                debug_log error "attachment is missing: [dict get $part name]"
                lappend out [::json::write object \
                    type [::json::write string text] \
                    text [::json::write string "\[image unavailable: [dict get $part name]\]"] \
                ]
            } else {
                lappend out [::json::write object \
                    type [::json::write string image_url] \
                    image_url [::json::write object \
                        url [::json::write string $url]] \
                ]
            }
        } else {
            lappend out [::json::write object \
                type [::json::write string text] \
                text [::json::write string [dict get $part text]] \
            ]
        }
    }
    return [::json::write array {*}$out]
}


# One tool as the API wants it: the schema that tells the model what the
# tool does and what its arguments are.
proc ::featherweight::json_tool args {
    if {[llength $args] % 2 != 0
        || [lsort [dict keys $args]] ni {
            {-command -description -name}
            {-command -description -name -param}
            {-command -description -name -param -required}
        }} {
        error {wrong # args: should be\
            "::featherweight::json_tool -command command -name name\
            -description description ?-param param ...? ?-required required?"}
    }

    set name [dict get $args -name]
    set description [dict get $args -description]
    set properties {}
    set required {}

    if {[dict exists $args -required]} {
        set required [dict get $args -required]
    }

    # foreach, not dict for: a tool's -param appears once per parameter, and
    # a dict would keep only one of them.
    foreach {option value} $args {
        switch -- $option {
            -command -
            -name -
            -description -
            -required {
                # -name, -description and -required were read above;
                # -command is ours -- the model never sees which procedure
                # runs the tool.
            }

            -param {
                if {[llength $value] != 3} {
                    error {each -param must be {name type description}}
                }

                lassign $value property type param_description

                if {[dict exists $properties $property]} {
                    error "duplicate parameter: $property"
                }

                dict set properties $property [list $type $param_description]
            }

            default {
                error "unknown option: $option"
            }
        }
    }

    set json_properties {}
    dict for {property schema} $properties {
        lassign $schema type param_description
        lappend json_properties $property [::json::write object \
            type [::json::write string $type] \
            description [::json::write string $param_description] \
        ]
    }

    set json_required {}
    foreach property $required {
        lappend json_required [::json::write string $property]
    }

    set parameters [::json::write object \
        type [::json::write string object] \
        properties [::json::write object {*}$json_properties] \
        required   [::json::write array {*}$json_required] \
    ]

    return [::json::write object \
        type [::json::write string "function"] \
        function [::json::write object \
            name [::json::write string $name] \
            description [::json::write string $description] \
            parameters $parameters \
        ] \
    ]
}


# One tool call as the API wants it: the name it calls and its arguments as
# the JSON text that came in.
proc ::featherweight::json_tool_call call {
    set function [dict get $call function]

    return [::json::write object \
        id [::json::write string [dict get $call id]] \
        type [::json::write string [dict get $call type]] \
        function [::json::write object \
            name [::json::write string [dict get $function name]] \
            arguments [::json::write string [dict get $function arguments]] \
        ] \
    ]
}


# One message as JSON, kept by memo so the next request need not work it out
# again.
proc ::featherweight::json_message message {
    return [memo json $message {json_message_uncached $message}]
}


# One message as JSON, worked out once here and kept by json_message.
proc ::featherweight::json_message_uncached message {
    set fields {}

    dict for {key value} $message {
        if {$key eq {tool_calls}} {
            set calls {}

            foreach call $value {
                lappend calls [::featherweight::json_tool_call $call]
            }

            lappend fields tool_calls [::json::write array {*}$calls]
        } elseif {$key eq {time} || $key eq {reasoning}} {
            # Unix time and the model's reasoning are ours, kept for the
            # record and the display. Neither is part of the message the API
            # expects: the reasoning is never sent back, and neither is
            # counted against the context budget.
        } elseif {$key eq {content} && [is_parts $value]} {
            lappend fields content [json_content $value]
        } else {
            lappend fields $key [::json::write string $value]
        }
    }

    return [::json::write object {*}$fields]
}


# The whole request body: the model to answer, the conversation to answer
# about, the tools it may call.
proc ::featherweight::json_payload {model json_tools messages} {
    set json_messages [lmap message $messages {
        ::featherweight::json_message $message
    }]

    set extra {}
    # Trial fork: pin the request to providers, with no fallbacks.
    variable config
    if {[dict exists $config provider_only] && [dict get $config provider_only] ne {}} {
        set only [lmap name [dict get $config provider_only] {::json::write string $name}]
        set extra [list provider [::json::write object \
            only [::json::write array {*}$only] \
            allow_fallbacks false]]
    }
    return [::json::write object \
        model [::json::write string $model] \
        messages [::json::write array {*}$json_messages] \
        reasoning [::json::write object effort [::json::write string low]] \
        tools [::json::write array {*}$json_tools] \
        tool_choice [::json::write string auto] \
        {*}$extra \
    ]
}


# --- the tools ---
#
# The model's answer can name tools; the harness runs one and appends its
# result like any other message. Each tool is a procedure taking the decoded
# arguments and returning the text to show.

proc ::featherweight::call_tool tool_call {
    variable config
    variable tools_available

    set id [dict get $tool_call id]
    set type [dict get $tool_call type]
    if {$type ne {function}} {
        return [dict create \
            role tool \
            tool_call_id $id \
            content "Error: unknown tool call type: [list $type]" \
        ]
    }
    set function [dict get $tool_call function]
    set name [dict get $function name]
    set arguments [dict get $function arguments]

    if {![dict exists $tools_available $name]} {
        return [dict create \
            role tool \
            tool_call_id $id \
            content "Error: unknown tool name: [list $name]" \
        ]
    }

    set command [dict get $tools_available $name -command]
    if {[catch {
        set result [{*}$command [::json::json2dict $arguments]]
    } result]} {
        set result "Error evaluating tool: $result"
    }

    return [dict create \
        role tool \
        tool_call_id $id \
        content $result \
    ]
}


# Refuse a tool call that left out something the tool requires.
proc ::featherweight::require_tool_arguments {arguments names} {
    foreach name $names {
        if {![dict exists $arguments $name]} {
            return -code error "The required \"$name\" argument was missing."
        }
    }
}


proc ::featherweight::read_text_file path {
    set channel [open $path r]
    if {[catch {
        fconfigure $channel -encoding utf-8 -translation lf
        read $channel
    } result]} {
        catch {close $channel}
        return -code error $result
    }
    close $channel
    return $result
}


# Write a file whole, through a temporary copy beside it: a crash mid-write
# leaves the old file rather than half of the new one.
proc ::featherweight::write_text_file {path content} {
    file mkdir [file dirname $path]
    set temporary "$path.[pid].[clock clicks].tmp"
    set channel {}

    if {[catch {
        set channel [open $temporary w]
        fconfigure $channel -encoding utf-8 -translation lf
        puts -nonewline $channel $content
        close $channel
        set channel {}
        if {$::tcl_platform(platform) ne {windows} && [file exists $path]} {
            file attributes $temporary -permissions [file attributes $path -permissions]
        }
        file rename -force $temporary $path
    } error options]} {
        if {$channel ne {}} {
            catch {close $channel}
        }
        catch {file delete -force $temporary}
        return -options $options $error
    }
}


# The read_file tool: a file's lines, numbered, from an offset.
proc ::featherweight::tool_read_file arguments {
    require_tool_arguments $arguments path
    set path [dict get $arguments path]
    if {[string trim $path] eq {}} {
        return {The required "path" argument was missing.}
    }

    set offset 1
    if {[dict exists $arguments offset]} {
        set offset [dict get $arguments offset]
    }
    if {![string is integer -strict $offset] || $offset < 1} {
        return {The "offset" argument must be a positive line number.}
    }

    set limit 2000
    if {[dict exists $arguments limit]} {
        set limit [dict get $arguments limit]
    }
    if {![string is integer -strict $limit] || $limit < 1} {
        return {The "limit" argument must be a positive number of lines.}
    }

    if {[catch {read_text_file $path} content]} {
        return "Could not read \"$path\": $content"
    }

    set content [string map {"\r\n" "\n"} $content]
    if {$content eq {}} {
        set lines {}
    } else {
        set lines [split $content \n]
        if {[string index $content end] eq "\n"} {
            set lines [lrange $lines 0 end-1]
        }
    }

    set total [llength $lines]
    set output "$path ($total lines)\n"
    if {$offset > $total} {
        append output "\nLine $offset is past the end of the file.\n"
        return $output
    }

    set first [expr {$offset - 1}]
    set last [expr {min($first + $limit - 1, $total - 1)}]
    set width [string length [expr {$last + 1}]]
    for {set index $first} {$index <= $last} {incr index} {
        append output [format "%*d\u2192%s\n" $width [expr {$index + 1}] [lindex $lines $index]]
    }

    if {$last < $total - 1} {
        set next [expr {$last + 2}]
        append output "\n(Lines $offset-[expr {$last + 1}] of $total. Read from offset $next for more.)\n"
    }
    return $output
}


# The write_file tool: a file, in full.
proc ::featherweight::tool_write_file arguments {
    require_tool_arguments $arguments {path content}
    set path [dict get $arguments path]
    if {[string trim $path] eq {}} {
        return {The required "path" argument was missing.}
    }

    set existed [file exists $path]
    if {[catch {write_text_file $path [dict get $arguments content]} error]} {
        return "Could not write \"$path\": $error"
    }
    return "[expr {$existed ? {Overwrote} : {Created}}] \"$path\"."
}


# The edit_file tool: one exact text replaced by another.
proc ::featherweight::tool_edit_file arguments {
    require_tool_arguments $arguments {path old_string new_string}
    set path [dict get $arguments path]
    if {[string trim $path] eq {}} {
        return {The required "path" argument was missing.}
    }

    set replace_all 0
    if {[dict exists $arguments replace_all]} {
        set replace_all [dict get $arguments replace_all]
        if {![string is boolean -strict $replace_all]} {
            return {The "replace_all" argument must be a boolean.}
        }
    }
    set old [dict get $arguments old_string]
    if {$old eq {}} {
        return {The "old_string" argument must not be empty; use write_file to replace the whole file.}
    }

    if {[catch {read_text_file $path} content]} {
        return "Could not read \"$path\": $content"
    }
    set count [expr {[string length $content] - [string length [string map [list $old {}] $content]]}]
    set count [expr {$count / [string length $old]}]

    if {$count == 0} {
        return "The search text was not found in \"$path\", so no change was made. It must match the current file contents exactly, character for character, including all whitespace, comments, and docstrings."
    }
    if {!$replace_all && $count != 1} {
        return "The text to replace appears $count times in \"$path\", so it is ambiguous and nothing was changed. Include enough surrounding text to match exactly once, or use \"replace_all\": true."
    }

    set content [string map [list $old [dict get $arguments new_string]] $content]
    if {[catch {write_text_file $path $content} error]} {
        return "Could not write \"$path\": $error"
    }
    if {$replace_all} {
        return "Replaced $count occurrence[expr {$count == 1 ? {} : {s}}] in \"$path\"."
    }
    return "Applied the edit to \"$path\"."
}


# The about tool: what this process is and where it runs.
proc ::featherweight::tool_about arguments {
    variable version
    variable process_started
    variable config

    set now [clock seconds]
    set script [file normalize [info script]]
    set tk_version [expr {[catch {package present Tk} version] ? {unavailable} : $version}]
    set lines [list  Featherweight  "Version: $version"  "Script: $script"  "Script modified: [clock format [file mtime $script] -format {%Y-%m-%d %H:%M:%S UTC} -gmt 1]"  "Process started: [clock format $process_started -format {%Y-%m-%d %H:%M:%S UTC} -gmt 1]"  {}  Time  "Now (UTC): [clock format $now -format {%Y-%m-%d %H:%M:%S UTC, %A} -gmt 1]"  "Now (local): [clock format $now -format {%Y-%m-%d %H:%M:%S %Z}]"  {}  System  "Tcl: [info patchlevel]"  "Tk: $tk_version"  "OS: $::tcl_platform(os)"  "OS version: $::tcl_platform(osVersion)"  "Architecture: $::tcl_platform(machine)"  "Platform: $::tcl_platform(platform)"  {}  Session  "Model: [dict get $config model]"  "Current session: [dict get $config current_session]"  "Project root: [pwd]"  ]
    return [join $lines \n]\n
}


# The namespace the run_code tool and /eval evaluate model code in. Its puts
# and exec shadow the Tcl commands of the same names, so that the output is
# captured and that no child process is left running without a timeout. This
# is not a sandbox: model code has the full interpreter and can reach the
# harness by name.
namespace eval ::run_code {
    variable output {}
    # The default limit on one command, in milliseconds. A command that runs
    # longer is killed and reported as timed out; see exec_timeout in the
    # configuration, and -timeout on a single call.
    variable timeout 60000
    # Names the per-run state of a command in flight.
    variable run_number 0
    # The harness's thread, for harness. Empty when the code runs in the
    # harness itself.
    variable harness_tid {}
}


# Stand in for puts while a tool's code runs: what the code prints is
# collected for the answer rather than sent to the process's stdout.
proc ::run_code::puts args {
    variable output

    set prefix {}
    if {[lindex $args 0] eq {-nonewline}} {
        set prefix -nonewline
        set args [lrange $args 1 end]
    }

    switch -- [llength $args] {
        1 {
            set channel stdout
            set text [lindex $args 0]
        }

        2 {
            lassign $args channel text
        }

        default {
            return -code error {wrong # args: should be "puts ?-nonewline? ?channelId? string"}
        }
    }

    if {$channel in {stdout stderr}} {
        append output $text [expr {$prefix eq {-nonewline} ? {} : "\n"}]
        return
    }

    tailcall ::puts {*}$prefix $channel $text
}


# Run a script in the harness and return its result. The code a tool call is
# given runs in a thread of its own, and this is its way to the harness's own
# state -- the configuration, the sessions, the interface -- which lives in
# the harness's thread. In the harness itself this is an eval at the top
# level. Tk belongs to the harness too, so widgets are built through this.
proc ::run_code::harness {script} {
    variable harness_tid

    if {$harness_tid eq {}} {
        return [uplevel #0 $script]
    }
    return [thread::send $harness_tid $script]
}


# Ask for more time than the call's limit allows, counted from now rather
# than from the start of the call: 30m, 2h. Meant for a computation that
# would otherwise be stopped when the limit runs out.
proc ::run_code::deadline {value} {
    harness [list ::featherweight::run_code_deadline [duration $value]]
}


# A duration as the model may write it -- 30, 30s, 2m, 1h -- in milliseconds.
proc ::run_code::duration {value} {
    if {![regexp {^([0-9]+(?:\.[0-9]+)?)(ms|s|m|h)?$} $value -> number unit]} {
        return -code error "bad duration [list $value]: use 30, 30s, 2m or 1h"
    }
    switch -- $unit {
        ms { return [expr {int($number)}] }
        m { return [expr {int($number * 60000)}] }
        h { return [expr {int($number * 3600000)}] }
        default { return [expr {int($number * 1000)}] }
    }
}


# The command that reports a timeout, in words a model can act on.
proc ::run_code::timeout_message {command milliseconds tree} {
    set seconds [expr {$milliseconds / 1000.0}]
    set message "timed out after [format %g $seconds]s: $command. The command was killed"
    if {$tree} {
        append message ", along with the processes it started"
    } else {
        append message "; processes it started may still be running"
    }
    return $message
}


# Run a tool's code and answer in the harness's words: what it printed, what
# it returned, and how it ended. Both the harness and a worker thread use
# this, so the answer reads the same wherever the code ran.
proc ::run_code::execute {code} {
    variable output

    set output {}
    set value {}
    catch {namespace eval ::run_code $code} value options

    return [join [list \
        <output>$output</output> \
        <result>$value</result>  \
        <options>$options</options> \
    ] "\n\n"]
}


# One tool call in a worker thread: run the code, then hand the answer to the
# harness, which is waiting for it.
proc ::run_code::call {code main_tid token} {
    variable harness_tid

    package require Thread
    set harness_tid $main_tid
    set payload [execute $code]
    thread::send -async $main_tid [list ::featherweight::run_code_arrived $token $payload]
}


# Run one command and wait for it, collecting what it wrote. Returns a
# failure name -- {}, timeout, start or status -- and fills the caller's
# variables: output is what the command wrote, error why it could not run or
# what closing the pipe said, pid the process, tree whether the processes it
# started were killed too, and errcode how it ended.
proc ::run_code::run {command ignorestderr limit outvar errvar pidvar treevar errcodevar} {
    upvar 1 $outvar output $errvar error $pidvar pid $treevar tree $errcodevar errcode

    set output {}
    set error {}
    set errcode [list CHILDSTATUS 0 1]
    set tree 0
    set pid {}

    # The command is run on its own: no setsid, since it forks at its own
    # discretion and the pid would then name a process already gone. A
    # timeout kills the command itself; see kill_command. The empty string
    # on its standard input stands in for the null device that exec gives a
    # command, so that one that reads input sees end of file instead of
    # waiting for input that will never come. The stderr redirect has to
    # stay the last word for open to accept it.
    set tail [concat [list << {}] $command]
    if {!$ignorestderr} {
        lappend tail 2>@1
    }

    if {[catch {open |$tail r} channel]} {
        set error "could not start: $channel"
        return start
    }
    fconfigure $channel -blocking 0
    set pid [lindex [pid $channel] 0]

    # Registered with the harness, so that the watchdog can find this
    # process and stop it whatever the code is doing meanwhile.
    catch {harness [list ::featherweight::run_code_child add $pid]}

    # The reader and the timer both wake the same variable, named per run so
    # that a command started from inside another does not disturb it.
    variable run_number
    set key r[incr run_number]
    set ::run_code::$key [dict create channel $channel eof 0 output {}]
    fileevent $channel readable [list ::run_code::drain $key]
    set ::run_code::wakeup$key 0
    set timer [after $limit [list set ::run_code::wakeup$key 1]]

    vwait ::run_code::wakeup$key
    after cancel $timer
    catch {harness [list ::featherweight::run_code_child remove $pid]}

    set state [set ::run_code::$key]
    set output [dict get $state output]

    if {![dict get $state eof]} {
        set tree [kill_command $pid]
        catch {close $channel}
        unset -nocomplain ::run_code::$key ::run_code::wakeup$key
        return timeout
    }

    # Back to blocking first: close on a pipeline left non-blocking returns
    # without reporting how the command ended, and the point of asking is to
    # see a command that failed.
    fconfigure $channel -blocking 1
    set status [catch {close $channel} error close_options]
    unset -nocomplain ::run_code::$key ::run_code::wakeup$key
    if {$status} {
        set errcode [dict get $close_options -errorcode]
        return status
    }
    return {}
}


# Read what a command has written so far.
proc ::run_code::drain {key} {
    set state [set ::run_code::$key]
    set channel [dict get $state channel]
    dict set state output [dict get $state output][read $channel]
    if {[eof $channel]} {
        fileevent $channel readable {}
        dict set state eof 1
        set ::run_code::wakeup$key 1
    }
    set ::run_code::$key $state
}


# Signal a process and its descendants. On Windows taskkill walks the tree;
# elsewhere ps finds the descendants. A process group cannot be relied on:
# setsid may fork, leaving the pid naming a process that has already exited.
proc ::run_code::signal_tree {pid signal} {
    if {$::tcl_platform(platform) eq {windows}} {
        if {$signal eq {KILL}} {
            catch {::exec taskkill /PID $pid /T /F}
        } else {
            catch {::exec taskkill /PID $pid /T}
        }
        return
    }
    foreach target [concat [list $pid] [descendants $pid]] {
        catch {::exec kill -$signal $target}
    }
}


# Stop a command and the processes it started: a polite signal first, so a
# well-behaved process can finish what it was doing, and a hard one after a
# second. Tcl has no kill of its own, so the system's kill sends the signal.
proc ::run_code::kill_command {pid} {
    signal_tree $pid TERM
    after 1000
    signal_tree $pid KILL
    return 1
}


# Every process below the given one, grandchildren included.
proc ::run_code::descendants {pid} {
    set out {}
    catch {
        set children [dict create]
        foreach line [split [::exec ps -eo pid=,ppid=] \n] {
            lassign $line child parent
            if {$child eq {} || $parent eq {}} {
                continue
            }
            dict lappend children $parent $child
        }
        set queue [list $pid]
        while {[llength $queue]} {
            set parent [lindex $queue end]
            set queue [lrange $queue 0 end-1]
            if {![dict exists $children $parent]} {
                continue
            }
            foreach child [dict get $children $parent] {
                lappend out $child
                lappend queue $child
            }
        }
    }
    return $out
}


# Run a command with a timeout, the way exec does but never hanging forever.
#
# Tcl's exec blocks the interpreter, so no timer can fire while it runs: the
# command is run as an asynchronous pipeline instead, and a watchdog in this
# interpreter -- which does get to run, because vwait services events -- kills
# it when the time is up. The call still blocks until the command is done or
# the timer goes off, so it reads like exec to the caller.
#
#   exec ?-timeout duration? ?-keepnewline? ?-ignorestderr? ?--? arg ...
#
# A duration may be 30, 30s, 2m or 1h. The default comes from exec_timeout in
# the configuration. On timeout the command and the processes it started are
# signalled -- taskkill walks the tree on Windows, ps finds the descendants
# elsewhere -- and the message says what was killed and what may be left.
proc ::run_code::exec args {
    variable timeout

    set limit $timeout
    set ignorestderr 0
    set keepnewline 0
    set command {}
    set want_timeout 0
    set parsing 1
    foreach arg $args {
        if {$want_timeout} {
            set limit [duration $arg]
            set want_timeout 0
            continue
        }
        if {$parsing} {
            switch -- $arg {
                -timeout {
                    set want_timeout 1
                    continue
                }
                -ignorestderr {
                    set ignorestderr 1
                    continue
                }
                -keepnewline {
                    set keepnewline 1
                    continue
                }
                -- {
                    set parsing 0
                    continue
                }
            }
            # Like exec's own options, the first word that is not one starts
            # the command: everything after it is a word of the command.
            set parsing 0
        }
        lappend command $arg
    }
    if {$want_timeout} {
        return -code error {missing value for -timeout}
    }
    if {![llength $command]} {
        return -code error {wrong # args: should be "exec ?-timeout duration? ?-keepnewline? ?-ignorestderr? ?--? arg ..."}
    }

    set started [clock milliseconds]
    set failure [run $command $ignorestderr $limit output error pid tree errcode]
    set elapsed [expr {[clock milliseconds] - $started}]

    # Like exec, the newline the command ended with is not part of the
    # answer unless -keepnewline says otherwise.
    if {!$keepnewline && [string match *\n $output]} {
        set output [string range $output 0 end-1]
    }

    if {$failure eq {timeout}} {
        return -options [dict create -code error -errorcode [list CHILDKILLED $pid SIGTERM]] \
            [timeout_message $command $limit $tree]
    }
    if {$failure eq {start}} {
        return -code error $error
    }
    if {$failure eq {status}} {
        # Like exec, a command that fails raises whatever it wrote; when it
        # wrote nothing, exec's own wording stands.
        set message $output
        if {$message eq {}} {
            set message $error
        }
        return -options [dict create -code error -errorcode $errcode] $message
    }
    return $output
}


# --- run_code: the code a tool call is given ---
#
# That code runs in a thread of its own, so that code which hangs -- a loop
# that never ends, a command that waits forever -- takes its own thread down
# and not the interface with it. A watchdog here in the harness stops the
# call when its deadline passes; see run_code_expire. The thread is thrown
# away after every call, so nothing in the interpreter one call does can
# reach the next. Without
# the Thread package the code runs here as it always has, with only the exec
# timeouts to bound it.

proc ::featherweight::run_code_support {} {
    # The code a worker thread needs: the ::run_code namespace and its
    # commands, taken from this interpreter so the two copies cannot drift
    # apart.
    set script "package require Thread\n"
    append script "namespace eval ::run_code \{\n"
    append script "    variable output \{\}\n"
    append script "    variable timeout [set ::run_code::timeout]\n"
    append script "    variable run_number 0\n"
    append script "    variable harness_tid \{\}\n"
    append script "\}\n"
    foreach name [lsort [info procs ::run_code::*]] {
        append script [list proc $name [info args $name] [info body $name]] \n
    }
    return $script
}


# The child processes of the call in flight, so that the watchdog can find
# them however the call is spending its time.
proc ::featherweight::run_code_child {operation pid} {
    variable run_code_children

    if {$operation eq {add}} {
        set run_code_children($pid) 1
    } else {
        unset -nocomplain run_code_children($pid)
    }
}


# The worker's answer to one call. An answer for a call the watchdog has
# already stopped is dropped: the timeout has the last word.
proc ::featherweight::run_code_arrived {token payload} {
    variable run_code_expired
    variable run_code_payload

    if {[info exists run_code_expired($token)]} { return }
    set run_code_payload($token) $payload
}


# ::run_code::deadline from model code: give the call in flight more time.
proc ::featherweight::run_code_deadline {milliseconds} {
    variable run_code_limit
    variable run_code_timer
    variable run_code_token

    if {![info exists run_code_token] || $run_code_token eq {}} {
        return -code error {no run_code call is in progress}
    }
    after cancel $run_code_timer
    set run_code_limit $milliseconds
    set run_code_timer [after $milliseconds \
        [list ::featherweight::run_code_expire $run_code_token timeout]]
    return $milliseconds
}


# Run one tool call to its answer. The call blocks here -- the model is
# waiting -- but the interface does not: this wait runs the event loop, which
# is what lets the watchdog and the Stop button work during a call.
proc ::featherweight::run_code_call code {
    variable config

    if {[catch {package require Thread}]} {
        return [::run_code::execute $code]
    }
    if {[catch {set worker [thread::create {thread::wait}]}]} {
        return [::run_code::execute $code]
    }

    variable run_code_children
    variable run_code_expired
    variable run_code_limit
    variable run_code_payload
    variable run_code_serial
    variable run_code_thread
    variable run_code_timer
    variable run_code_token

    array unset run_code_children
    set token r[incr run_code_serial]
    set run_code_token $token

    set run_code_thread $worker
    thread::send $worker [run_code_support]

    set run_code_limit [expr {[dict get $config run_code_timeout] * 1000}]
    set run_code_timer [after $run_code_limit \
        [list ::featherweight::run_code_expire $token timeout]]
    thread::send -async $worker [list ::run_code::call $code [thread::id] $token]

    vwait ::featherweight::run_code_payload($token)
    after cancel $run_code_timer
    catch {thread::release $worker}
    set run_code_token {}
    set result $run_code_payload($token)
    unset -nocomplain run_code_payload($token) run_code_expired($token)
    return $result
}


# Stop a call that has timed out or been stopped by the user. First signal
# its child processes, which the operating system can kill regardless of
# what the thread is doing. Then cancel the evaluation, drop the thread,
# and report the timeout or stop.
proc ::featherweight::run_code_expire {token reason} {
    variable run_code_children
    variable run_code_expired
    variable run_code_token

    if {![info exists run_code_token] || $run_code_token ne $token} { return }

    set run_code_expired($token) 1
    foreach pid [array names run_code_children] {
        ::run_code::signal_tree $pid TERM
    }
    after 1000 [list ::featherweight::run_code_kill $token]
    after 2000 [list ::featherweight::run_code_abandon $token $reason]
}


# The watchdog's second step, a second after the first: the hard signal, and
# the thread's own evaluation cancelled under it.
proc ::featherweight::run_code_kill token {
    variable run_code_children
    variable run_code_thread
    variable run_code_token

    if {![info exists run_code_token] || $run_code_token ne $token} { return }

    foreach pid [array names run_code_children] {
        ::run_code::signal_tree $pid KILL
    }
    catch {thread::cancel $run_code_thread}
}


# The watchdog's last step: the thread is dropped and the call reported for
# what it was -- timed out, or stopped by the user.
proc ::featherweight::run_code_abandon {token reason} {
    variable run_code_children
    variable run_code_limit
    variable run_code_payload
    variable run_code_thread
    variable run_code_token

    if {![info exists run_code_token] || $run_code_token ne $token} { return }

    catch {thread::cancel $run_code_thread}
    array unset run_code_children

    if {$reason eq {stopped}} {
        set message {the call was stopped before the code finished.}
    } else {
        set message "timed out after [format %g [expr {$run_code_limit / 1000.0}]]s:"
        append message { the code was still running and was stopped.}
        append message { Ask for more time with ::run_code::deadline if it needs it;}
        append message { commands can pass -timeout to exec.}
    }
    set run_code_payload($token) [join [list \
        <output></output> \
        <result>$message</result> \
        <options>[dict create -code 1 -errorcode {FEATHERWEIGHT RUN_CODE_TIMEOUT}]</options> \
    ] "\n\n"]
}


# The run_code tool: run the model's code and answer with what it printed,
# what it returned, and how it ended. The code runs in a thread of its own
# where the Thread package is there -- see the run_code section above -- and
# in the harness itself where it is not.
proc ::featherweight::tool_run_code arguments {
    variable config

    if {[dict get $config debug]} {
        # Folded, with the tool's name in its header, the way a call is: the
        # code it runs is often many lines and otherwise drowns the log
        # around it. The text keeps the name in it for the log file.
        debug_log tool "run_code $arguments" run_code
    }

    return [run_code_call [dict get $arguments code]]
}


# --- recall: the session record as memory ---
#
# Compaction loses the model's own results. A summary keeps what the user
# said and none of what the work found out; asked about it later the model
# can only say it does not know, with the answer one search away on disk.
# The record keeps all of it -- that is what the markers are for -- and
# recall searches the record. The design is Strument's
# (doc/experiments/2026-10-recall and the studies after it): shaped like
# grep, hits newest first with ids that open a message whole, matches still
# in the conversation counted rather than shown, and recall's own results
# left out so a search cannot find its previous answers. The query is raw
# FTS5 rather than a regexp: an ephemeral SQLite database in memory indexes
# the whole record in about ten milliseconds, and models write FTS5.

# The record as search entries, one per message, numbered tN.M by turn: a
# turn is what a genuine user message starts, and t4.3 is the third entry of
# the fourth. Markers contribute the summary they carry. Recall's own
# tool results are left out: they are copies of the record, so a search
# would find its own previous answers and make a fact look better attested
# than it is.
proc ::featherweight::recall_entries {name} {
    set path [session_path $name]
    if {![file exists $path]} {
        return -code error "no session [list $name]"
    }
    set records [session_records [::fileutil::cat -encoding utf-8 $path]]

    # What each tool call was named, so a result can say which tool made it.
    set tool_names {}
    foreach record $records {
        if {![is_message $record] || ![dict exists $record tool_calls]} {
            continue
        }
        foreach call [dict get $record tool_calls] {
            if {[dict exists $call id] && [dict exists $call function name]} {
                dict set tool_names [dict get $call id] [dict get $call function name]
            }
        }
    }

    set stream {}
    foreach record $records {
        if {[is_marker $record]} {
            if {[dict exists $record summary]} {
                foreach message [dict get $record summary] {
                    lappend stream $message
                }
            }
            continue
        }
        lappend stream $record
    }

    set entries {}
    set turn 1
    set index 0
    set seen_user 0
    foreach message $stream {
        set role [dict get $message role]
        set tool {}
        if {$role eq {tool}} {
            set call {}
            if {[dict exists $message tool_call_id]} {
                set call [dict get $message tool_call_id]
            }
            if {[dict exists $tool_names $call]} {
                set tool [dict get $tool_names $call]
            }
            if {$tool eq {recall}} {
                continue
            }
        } elseif {$role eq {user} && ![is_harness_note $message]} {
            if {$seen_user} {
                incr turn
                set index 0
            }
            set seen_user 1
        }

        set text {}
        catch {set text [content_display [dict get $message content]]}
        if {[dict exists $message reasoning]} {
            # The reasoning is the model's own prose, which is what a search
            # of the record is most often for.
            append text \n [dict get $message reasoning]
        }
        if {[dict exists $message tool_calls]} {
            foreach call [dict get $message tool_calls] {
                catch {
                    append text \n [dict get $call function name] { } \
                        [dict get $call function arguments]
                }
            }
        }
        if {[string trim $text] eq {}} {
            continue
        }

        set time {}
        if {[dict exists $message time]} {
            set time [dict get $message time]
        }
        incr index
        lappend entries [dict create id t$turn.$index turn $turn \
            role $role tool $tool time $time record $message text $text]
    }
    return $entries
}


# Whether an entry is in the scope a search asked for. Nearly all the noise
# is in tool output -- the files the model read -- while the model's own
# results are stated in its prose, so the two are searched apart. A tool
# call is what the model said, so it is conversation; only its result is
# tools.
proc ::featherweight::recall_in_scope {entry from} {
    if {$from eq {all}} {
        return 1
    }
    if {$from eq {tools}} {
        return [expr {[dict get $entry role] eq {tool}}]
    }
    return [expr {[dict get $entry role] ne {tool}}]
}


# A message's line, shortened around the query's first mark: a minified file
# or a long JSON line would otherwise be the whole result.
proc ::featherweight::recall_clip {line} {
    if {[string length $line] <= 300} {
        return [string map [list \u0001 {} \u0002 {}] $line]
    }
    set at [string first \u0001 $line]
    set from 0
    if {$at > 100} {
        set from [expr {$at - 100}]
    }
    set to [expr {$from + 300}]
    if {$to > [string length $line]} {
        set to [string length $line]
    }
    set line [string range $line $from $to]
    set out [string map [list \u0001 {} \u0002 {}] $line]
    if {$from > 0} {
        set out "\u2026$out"
    }
    if {$to < [string length $line]} {
        append out \u2026
    }
    return $out
}


# The lines of one message the query marked, with their context, as grep -C
# shows them: at most limit matches, lines numbered within the message.
proc ::featherweight::recall_lines {marked context limit id} {
    set lines [split $marked \n]
    set matches {}
    set number 0
    foreach line $lines {
        if {[string first \u0001 $line] >= 0} {
            lappend matches $number
        }
        incr number
    }
    if {$matches eq {}} {
        # The query matches across lines; show where its first mark falls.
        set before [string first \u0001 $marked]
        set matches [expr {[llength [split [string range $marked 0 $before] \n]] - 1}]
    }

    set out {}
    set last -1
    set shown 0
    foreach match $matches {
        if {$shown >= $limit} {
            append out "  ([expr {[llength $matches] - $shown}] more matching lines: recall with id=[list $id])\n"
            break
        }
        incr shown
        set from [expr {$match - $context < $last + 1 ? $last + 1 : $match - $context}]
        set to [expr {$match + $context > [llength $lines] - 1 ? [llength $lines] - 1 : $match + $context}]
        if {$last >= 0 && $from > $last + 1} {
            append out "  ...\n"
        }
        for {set i $from} {$i <= $to} {incr i} {
            append out [format "  %d: %s\n" [expr {$i + 1}] \
                [recall_clip [lindex $lines $i]]]
        }
        set last $to
    }
    return $out
}


# One message whole, as the record holds it: about 400 lines or 40000
# characters. marked is the message's text with the query's marks in it; the
# lines carrying one are shown with a >.
proc ::featherweight::recall_open {entry marked} {
    set out "[dict get $entry id] \u00b7 turn [dict get $entry turn] \u00b7 [recall_speaker $entry] \u00b7 [recall_time $entry]\n"
    set lines [split [dict get $entry text] \n]
    # marked contains the query's marks; the display shows the text without
    # them.
    set marks {}
    set markline 0
    foreach line [split $marked \n] {
        if {[string first \u0001 $line] >= 0} {
            lappend marks $markline
        }
        incr markline
    }
    set runes 0
    set number 0
    foreach line $lines {
        if {$number >= 400 || $runes > 40000} {
            append out "([expr {[llength $lines] - $number}] more lines not shown)\n"
            break
        }
        incr number
        set mark { }
        if {[expr {$number - 1}] in $marks} {
            set mark >
        }
        append out [format "%s %d: %s\n" $mark $number \
            [string map [list \u0001 {} \u0002 {}] $line]]
        incr runes [string length $line]
    }
    return $out
}


proc ::featherweight::recall_speaker entry {
    if {[dict get $entry role] eq {tool}} {
        return "tool [dict get $entry tool]"
    }
    return [dict get $entry role]
}


proc ::featherweight::recall_time entry {
    set time [dict get $entry time]
    if {$time eq {}} {
        return {}
    }
    return [format_time $time]
}


# Open one entry by id, whether or not it matched a search.
proc ::featherweight::recall_open_id {entries id} {
    foreach entry $entries {
        if {[dict get $entry id] eq $id} {
            return [recall_open $entry {}]
        }
    }
    return "No message has the id [list $id]. Ids look like \"t4.3\" and come from a search's results."
}


# Search the session record. The query is an FTS5 full-text query; a hit is
# a message, newest first, and its id opens it whole.
proc ::featherweight::tool_recall arguments {
    foreach name {pattern from newest id session} {
        set $name {}
        if {[dict exists $arguments $name]} {
            set $name [dict get $arguments $name]
        }
    }
    if {$pattern eq {} && $id eq {}} {
        return {Give a "pattern" to search for, or the "id" of a message to see whole.}
    }
    if {$from eq {}} {
        set from conversation
    }
    if {$from ni {conversation tools all}} {
        return "\"from\" must be \"conversation\", \"tools\" or \"all\", not [list $from]."
    }
    if {![string is integer -strict $newest] || $newest < 1} {
        set newest 10
    }
    if {$newest > 50} {
        set newest 50
    }
    if {$session eq {}} {
        set session [current_session_name]
    }
    if {[catch {recall_entries $session} entries]} {
        return $entries
    }

    # A hit's id passed as the pattern can only match itself, and ids are
    # not text in the record: open the message instead. A live session did
    # exactly this, with an id escaped as a pattern, after being refused.
    if {$id eq {} && [regexp {^t([0-9]+)\\?\.([0-9]+)$} $pattern -> turn message]} {
        set id t$turn.$message
        return "[list $pattern] is a message id, not text in the record, so it was opened as id=[list $id].\n[recall_open_id $entries $id]"
    }
    if {$pattern eq {}} {
        return [recall_open_id $entries $id]
    }
    if {[catch {package require sqlite3} missing]} {
        return "The record cannot be searched: $missing"
    }

    # The record as an ephemeral index, thrown away when the call is done:
    # over a session of a thousand messages this is about ten milliseconds,
    # and nothing has to be kept in step with the file as it grows.
    sqlite3 recall_db :memory:
    set code [catch {
        recall_db eval {
            CREATE VIRTUAL TABLE hit USING
                fts5(text, tokenize = 'unicode61 remove_diacritics 2')
        }
        recall_db eval {BEGIN}
        set rowid 0
        foreach entry $entries {
            incr rowid
            set text [dict get $entry text]
            recall_db eval {INSERT INTO hit(rowid, text) VALUES($rowid, $text)}
        }
        recall_db eval {COMMIT}

        set note {}
        set query $pattern
        if {[catch {
            set rows [recall_db eval {
                SELECT rowid, highlight(hit, 0, char(1), char(2))
                FROM hit WHERE hit MATCH :query
            }]
        } why]} {
            # Not valid FTS5. Search for the words as a phrase, and say so:
            # a model that meant a query should know it was not used as one.
            set note "The query is not valid FTS5, so it was searched for as a phrase.\n"
            set query "\"[string map [list "\"" "\"\""] $pattern]\""
            set rows [recall_db eval {
                SELECT rowid, highlight(hit, 0, char(1), char(2))
                FROM hit WHERE hit MATCH :query
            }]
        }
        list $note $rows
    } result]
    recall_db close
    if {$code} {
        return "The record could not be searched: $result"
    }
    lassign $result note rows

    set marked {}
    foreach {rowid text} $rows {
        dict set marked $rowid $text
    }

    # What the conversation holds now is not shown: the model can see it.
    array set visible {}
    foreach message [current_messages] {
        set visible([list $message]) 1
    }

    set hits {}
    set in_view 0
    set out_of_scope 0
    for {set i [expr {[llength $entries] - 1}]} {$i >= 0} {incr i -1} {
        if {![dict exists $marked [expr {$i + 1}]]} {
            continue
        }
        set entry [lindex $entries $i]
        if {![recall_in_scope $entry $from]} {
            incr out_of_scope
            continue
        }
        set key [list [dict get $entry record]]
        if {[info exists visible($key)]} {
            incr in_view
            continue
        }
        lappend hits [list $entry [dict get $marked [expr {$i + 1}]]]
    }

    if {$id ne {}} {
        foreach hit $hits {
            lassign $hit entry text
            if {[dict get $entry id] eq $id} {
                return $note[recall_open $entry $text]
            }
        }
        return $note[recall_open_id $entries $id]
    }

    set out $note
    set shown [expr {[llength $hits] < $newest ? [llength $hits] : $newest}]
    set turns 0
    foreach entry $entries {
        if {[dict get $entry turn] > $turns} {
            set turns [dict get $entry turn]
        }
    }

    append out "Recall [list $pattern] in session $session\n"
    if {$shown == 0} {
        append out {No matches outside this conversation}
    } else {
        append out "[expr {$shown < [llength $hits] ? "Showing $shown of [llength $hits] messages" : "[llength $hits] messages"}] outside this conversation, newest first"
        if {$shown < [llength $hits]} {
            append out { (raise newest, or narrow the query, for more)}
        }
    }
    append out .

    # Replies the cutoff hid are named: a search for a word an answer used
    # matches tool output too, and the answer can fall below the cutoff.
    set hidden {}
    foreach hit [lrange $hits $shown end] {
        lassign $hit entry text
        if {[dict get $entry role] eq {assistant}} {
            lappend hidden [dict get $entry id]
        }
    }
    if {$hidden ne {}} {
        set more {}
        if {[llength $hidden] > 10} {
            set more " and [expr {[llength $hidden] - 10}] more"
            set hidden [lrange $hidden 0 9]
        }
        append out " Not shown, your own replies: [join $hidden {, }]$more; pass one as id to see it."
    }
    if {$in_view > 0} {
        append out " $in_view more in messages still in this conversation (not shown)."
    }
    if {$out_of_scope > 0} {
        set other [expr {$from eq {conversation} ? {tool output} : {the conversation}}]
        append out " $out_of_scope more in $other: pass from=\"all\" to include them."
    }
    append out \n

    foreach hit [lrange $hits 0 [expr {$shown - 1}]] {
        lassign $hit entry text
        append out "\n[dict get $entry id] \u00b7 turn [dict get $entry turn] of $turns \u00b7 [recall_speaker $entry] \u00b7 [recall_time $entry]\n"
        append out [recall_lines $text 2 3 [dict get $entry id]]
    }
    return $out
}


# --- one turn ---
#
# The loop a harness is: a request goes out, the response may name tools,
# each tool runs and its result is appended, and the next request goes out
# until the answer names none. Failures that another attempt can fix -- a
# dropped connection, a server asking for patience -- are tried again.

# The answer as we store it: its content, its tool calls, and its reasoning
# for the record -- the reasoning is never sent back.
proc ::featherweight::clean_assistant_message {msg} {
    set out [dict create role assistant]
    if {[dict exists $msg content]} {
        set content [dict get $msg content]
        if {$content ni {"" null}} {
            dict set out content $content
        }
    }
    if {[dict exists $msg tool_calls]} {
        dict set out tool_calls [dict get $msg tool_calls]
    }
    # The model's reasoning is kept for the record and shown folded, but it
    # is never sent back: json_message leaves it out, the way it leaves out
    # time. Providers name it either thing.
    foreach key {reasoning reasoning_content} {
        if {[dict exists $msg $key] && [dict get $msg $key] ni {"" null}} {
            dict set out reasoning [dict get $msg $key]
            break
        }
    }
    return $out
}


# Turn a response body into the assistant message to store, or fail with a
# message saying what was wrong with it. The GUI's turn and script mode's
# replay both read responses through this, so a malformed answer is reported
# the same way in either.
proc ::featherweight::response_message {data} {
    if {[catch {json::json2dict $data} response]} {
        error "JSON decode failed: $response"
    }
    if {![dict exists $response choices] || [llength [dict get $response choices]] == 0} {
        error {response has no choices}
    }
    set choice [lindex [dict get $response choices] 0]
    if {![dict exists $choice message]} {
        error {choice has no message}
    }
    return [clean_assistant_message [dict get $choice message]]
}


# Apply one assistant message to the session: store it, show it, and run the
# tool calls it made, storing their results. Returns 1 when the turn should
# continue with another request. The GUI's async turn and script mode's replay
# both end here, so a tool call behaves the same way in either. Storing comes
# first so the message's own time is what the interface shows beside it.
proc ::featherweight::apply_message {cleaned} {
    variable busy

    set cleaned [append_message $cleaned]
    set time [message_time $cleaned]
    # The reasoning leads the message and carries its date; the blocks that
    # follow are one message, so they take their labels without repeating it.
    set thinking [message_reasoning $cleaned]
    if {$thinking ne {}} {
        ui_thinking $thinking $time
        set time {}
    }
    if {[dict exists $cleaned content]} {
        ui_assistant [dict get $cleaned content] $time
    }

    if {![dict exists $cleaned tool_calls]} {
        return 0
    }
    foreach call [dict get $cleaned tool_calls] {
        if {!$busy} { return 0 }
        set name {}
        catch {set name [dict get $call function name]}
        debug_log tool "call: $call" $name
        ui_tool_code $call $time
        set result [call_tool $call]
        if {!$busy} { return 0 }
        set result [append_message $result]
        ui_tool [dict get $result content] [message_time $result]
        # A tool can change what the title shows: run_code can cd, and any
        # tool can change harness state.
        update_window_title
    }
    return $busy
}


# What went wrong with a curl run, from the trailing --write-out line if it is
# there, or from the error close reported. That line holds the HTTP status,
# curl's exit code and curl's own message, and it survives a run that failed --
# so the reason is named rather than guessed.
proc ::featherweight::curl_error {output close_error} {
    set marker [string last "\n" $output]
    if {$marker >= 0} {
        set metadata [string range $output [expr {$marker + 1}] end]
        set error_msg [lassign $metadata status exit_code]
        if {[string is integer -strict $exit_code] && $exit_code != 0} {
            return "curl ($exit_code): $error_msg"
        }
    }
    return "curl request failed: $close_error"
}


# Retry after a pause until the configured retry limit is reached. Only
# potentially recoverable failures come here: curl could not connect,
# timed out, or lost its connection; or the server returned 429 or 5xx.
# The pause grows 1, 2, 4, 8, 16 seconds.
proc ::featherweight::retry_request {why} {
    variable busy
    variable config
    variable retry_count

    incr retry_count
    set retries 5
    if {[dict exists $config retries]} {
        set retries [dict get $config retries]
    }
    if {$retry_count > $retries} {
        debug_log error "giving up after $retries retries: $why"
        set retry_count 0
        set_ui_busy 0
        return
    }

    set delay [expr {1 << ($retry_count - 1)}]
    if {$delay > 16} {
        set delay 16
    }
    debug_log error "retry $retry_count of $retries in $delay s: $why"
    after [expr {$delay * 1000}] [list [namespace current]::retry_pending]
}


# One scheduled retry. The turn may have been stopped while it waited.
proc ::featherweight::retry_pending {} {
    variable busy
    if {!$busy} { return }
    request_async
}


# Send the request. Everything that builds it is inside the catch: an error
# here stops the turn with a reason, never a silent wait. The response
# arrives at request_readable.
proc ::featherweight::request_async {} {
    variable busy
    variable config
    variable curl_data
    variable json_tools
    variable request_channels

    if {!$busy} { return }
    # Everything that builds the request is inside this catch. An error that
    # escaped it is what left the interface saying "Working..." forever, with
    # Tk's dialog up behind it and nothing in the log but the traceback.
    if {[catch {
        set messages [prepare_request]
        set payload [json_payload [dict get $config model] $json_tools $messages]
    } error]} {
        debug_log error $error
        set_ui_busy 0
        return
    }
    debug_log request $payload

    if {[catch {
        set ch [open |[list curl \
            --data-binary @- << $payload \
            --header "Authorization: Bearer [api_key]" \
            --header {Content-Type: application/json} \
            --header {X-OpenRouter-Title: Featherweight} \
            --referer https://dbohdan.com/featherweight \
            --request POST \
            --show-error \
            --silent \
            --max-time [dict get $config timeout] \
            --write-out "\n%{http_code} %{exitcode} %{errormsg}" \
            [dict get $config endpoint] \
        ]]
    } error]} {
        debug_log error "could not start curl request: $error"
        set_ui_busy 0
        return
    }

    set curl_data($ch) {}
    set request_channels($ch) 1
    fconfigure $ch -blocking 0
    fileevent $ch readable [list [namespace current]::request_readable $ch]
}


# The response arriving a piece at a time; at its end, the reply is stored
# and the turn continues.
proc ::featherweight::request_readable ch {
    variable busy
    variable curl_data
    variable request_channels
    variable retry_count

    if {!$busy || ![info exists request_channels($ch)]} { return }

    # Accumulate available output. Wait for EOF before parsing the response.
    if {[catch {read $ch} chunk]} {
        fileevent $ch readable {}
        unset -nocomplain curl_data($ch)
        unset request_channels($ch)
        catch {close $ch}
        retry_request "could not read curl response: $chunk"
        return
    }
    append curl_data($ch) $chunk
    if {![eof $ch]} {
        return
    }

    fileevent $ch readable {}
    set output $curl_data($ch)
    unset curl_data($ch)
    unset request_channels($ch)

    if {[catch {close $ch} error]} {
        if {!$busy} { return }
        retry_request [curl_error $output $error]
        return
    }
    if {!$busy} { return }

    # Separate the status appended by --write-out from the response body.
    set marker [string last "\n" $output]
    if {$marker < 0} {
        debug_log error {curl response did not include an HTTP status}
        set_ui_busy 0
        return
    }

    set metadata [string range $output [expr {$marker + 1}] end]
    set error_msg [lassign $metadata status exit_code]

    if {![string is integer -strict $status]} {
        debug_log error "invalid HTTP status from curl: $status"
        set_ui_busy 0
        return
    }

    if {![string is integer -strict $exit_code]} {
        debug_log error "invalid exit code from curl: $exit_code"
        set_ui_busy 0
        return
    }

    if {$exit_code != 0} {
        retry_request "curl ($exit_code): $error_msg"
        return
    }
    set retry_count 0

    set data [string range $output 0 [expr {$marker - 1}]]

    debug_log response "HTTP $status\n$data"

    if {$status != 200} {
        # 429 and 5xx are the server asking for patience; anything else is
        # the request being wrong and a retry would fail the same way.
        if {$status == 429 || $status >= 500} {
            retry_request "HTTP $status: $data"
            return
        }
        debug_log error "HTTP $status: $data"
        set_ui_busy 0
        return
    }

    if {[catch {response_message $data} cleaned]} {
        debug_log error $cleaned
        set_ui_busy 0
        return
    }

    if {[apply_message $cleaned]} {
        request_async
        return
    }

    set_ui_busy 0
}


# A Unix time as text, in the configured format. Messages store Unix time;
# this is what turns it into a line for the interface, the debug log, and the
# session listings.
# --- showing the conversation ---
#
# Every message is a heading -- its time, then its label in bold -- and a
# body: the words in the color of whoever said them, a tool call as its
# name and its parameters, the model's reasoning folded shut.

proc ::featherweight::format_time {time} {
    variable config

    set format {%Y-%m-%d %H:%M:%S %z}
    if {[dict exists $config timestamp_format]} {
        set format [dict get $config timestamp_format]
    }
    return [clock format $time -format $format]
}


# When a message was made, as a Unix time, or {} for messages written before
# timestamps existed, which are shown without one.
proc ::featherweight::message_time {message} {
    if {[dict exists $message time]
            && [string is integer -strict [dict get $message time]]} {
        return [dict get $message time]
    }
    return {}
}


# The model's own reasoning for a message, trimmed, or empty when it has
# none. Kept for the record and shown folded; never sent back.
proc ::featherweight::message_reasoning message {
    if {![dict exists $message reasoning]} {
        return {}
    }
    return [string trim [dict get $message reasoning]]
}


# A message's content as one piece of text: the words, and a marker for each
# image. Used for display, for the listings, and for what a fold quotes.
proc ::featherweight::content_display content {
    if {![is_parts $content]} {
        return $content
    }
    set lines {}
    foreach part $content {
        if {[dict get $part type] eq {image}} {
            lappend lines "\[image: [dict get $part name]\]"
        } else {
            lappend lines [dict get $part text]
        }
    }
    return [join $lines \n]
}


# Put text in the conversation, in the style of one of its tags.
proc ::featherweight::ui_append {tag text} {
    variable W
    if {![dict exists $W chat]} { return }
    set t [dict get $W chat]
    $t configure -state normal
    $t insert end $text $tag
    $t configure -state disabled
    $t see end
}


# The heading of a message: the time on its own line, then the label in
# bold. The two on one line would fight for it: a long label like Tool call:
# edit_file pushed the time far to the right.
proc ::featherweight::ui_label {label time} {
    if {$time eq {}} {
        ui_append role "$label\n"
        return
    }
    ui_append timestamp "[format_time $time]\n"
    ui_append role "$label\n"
}


proc ::featherweight::ui_user {text {time {}}} {
    ui_label "You" $time
    ui_append user "$text\n\n"
}


proc ::featherweight::ui_assistant {text {time {}}} {
    ui_label "Assistant" $time
    ui_append assistant "$text\n\n"
}


# A tool call's arguments as lines for the conversation: one parameter per
# line, in the tool's own order, the names padded so the values line up. The
# padding is spaces -- a Tk text has no tab stops, so a tab would advance to
# a multiple of the font's space and land anywhere -- and a value that runs
# over one line goes on its own line beneath the name, not indented: a code
# block or a path is read as it was written.
proc ::featherweight::tool_arguments_text {name raw} {
    set order {}
    foreach key [lsort [dict keys $::featherweight::tools_available]] {
        if {$key eq $name} {
            set def [dict get $::featherweight::tools_available $key]
            foreach {option value} $def {
                if {$option eq {-param}} {
                    lappend order [lindex $value 0]
                }
            }
        }
    }

    # The arguments as they were written: an array is recognized from the
    # raw text rather than the decoded value's shape, because a string with
    # spaces or newlines looks like a list and is not one. The pattern below
    # avoids a bare close brace, which would end this procedure body.
    set raw_values {}
    set arguments {}
    if {[catch {json::json2dict $raw} value]} {
        return $raw
    }
    set arguments $value
    if {$arguments eq {}} {
        return $raw
    }
    set raw_values {}
    foreach line [split $raw \n] {
        foreach {full key value} \
                [regexp -all -inline {"([^"]*)"[ ]*:[ ]*("(?:[^"\\]|\\.)*"|\[[^[]*\]|[^,]*)} $line] {
            dict set raw_values $key [string trim $value]
        }
    }

    set keys {}
    foreach key $order {
        if {[dict exists $arguments $key]} {
            lappend keys $key
        }
    }
    foreach key [lsort [dict keys $arguments]] {
        if {$key ni $keys} {
            lappend keys $key
        }
    }

    set width 0
    foreach key $keys {
        if {[string length $key] > $width} {
            set width [string length $key]
        }
    }

    set blocks {}
    foreach key $keys {
        set value [dict get $arguments $key]
        set as_written {}
        if {[dict exists $raw_values $key]} {
            set as_written [dict get $raw_values $key]
        }
        if {[string match "\[*" $as_written]} {
            # A JSON array: show it as one.
            set value "\[[join $value {, }]\]"
        }
        if {[string first \n $value] >= 0} {
            lappend blocks "$key:\n$value"
        } else {
            # Two spaces past the longest name, so the values line up under
            # it, counted in characters: format's %-*s is meant for display
            # columns and misaligns past the first.
            lappend blocks "$key:[string repeat { } [expr {$width + 2 - [string length $key]}]]$value"
        }
    }
    # A blank line between arguments: where a value runs over several lines,
    # this is the only mark of where the next name begins.
    set out [join $blocks "\n\n"]
    append out \n
    return $out
}


# One tool call in the conversation: run_code shows its code, any other tool
# shows its name and its parameters.
proc ::featherweight::ui_tool_code {call {time {}}} {
    set function [dict get $call function]
    set name [dict get $function name]
    set arguments [dict get $function arguments]
    if {$name eq {run_code}} {
        if {![catch {json::json2dict $arguments} decoded]
                && [dict exists $decoded code]} {
            set arguments [dict get $decoded code]
        }
        ui_label "Tool call: run_code" $time
        ui_append tool "$arguments\n\n"
        return
    }
    ui_label "Tool call: $name" $time
    ui_append tool "[tool_arguments_text $name $arguments]\n"
}


proc ::featherweight::ui_tool {text {time {}}} {
    ui_label "Result" $time
    ui_append tool "$text\n\n"
}


# Show the model's reasoning folded, separate from the answer. The marker
# opens it like a debug-log entry. The date precedes the reasoning; later
# blocks of the same message have labels but do not repeat the date.
proc ::featherweight::ui_thinking {text {time {}}} {
    variable W
    variable thinking_open
    variable thinking_serial

    incr thinking_serial
    set n $thinking_serial
    set thinking_open($n) 0
    if {$time eq {}} {
        ui_append role {Thinking }
    } else {
        ui_append timestamp "[format_time $time]\n"
        ui_append role {Thinking }
    }
    ui_append [list role think$n] {[+]}
    ui_append role "\n"
    ui_append [list thinking body$n] "$text\n\n"

    if {![dict exists $W chat]} { return }
    set w [dict get $W chat]
    $w tag configure body$n -elide 1
    # The marker's own tag carries its color as well as the underline: Tk
    # draws an underline in the color of the tag that sets it. Its font is
    # fixed width, so the closing bracket stays put between [+ and [−: in a
    # proportional font the minus sign is a little wider than the plus.
    $w tag configure think$n -foreground #555555 -underline 1 -font TkFixedFont
    $w tag bind think$n <Button-1> [list [namespace current]::ui_thinking_toggle $n]
}


# Open or close one folded reasoning block.
proc ::featherweight::ui_thinking_toggle n {
    variable W
    variable thinking_open

    if {![dict exists $W chat]} { return }
    set w [dict get $W chat]
    set open $thinking_open($n)
    $w configure -state normal
    if {$open} {
        $w tag configure body$n -elide 1
        set marker {[+]}
    } else {
        $w tag configure body$n -elide 0
        set marker "\[\u2212\]"
    }
    set ranges [$w tag ranges think$n]
    if {[llength $ranges] >= 2} {
        lassign $ranges start end
        set tags [$w tag names $start]
        $w delete $start $end
        $w insert $start $marker $tags
        $w see $start
    }
    set thinking_open($n) [expr {!$open}]
    $w configure -state disabled
}


# Draw the conversation from the session: every message in order, each tool
# call under the message that made it.
proc ::featherweight::replay_messages {} {
    variable config

    set pending_tool_calls {}
    set pending_time {}
    foreach message [current_messages] {
        set time [message_time $message]
        switch -- [dict get $message role] {
            user {
                ui_user [content_display [dict get $message content]] $time
            }
            assistant {
                set thinking [message_reasoning $message]
                if {$thinking ne {}} {
                    ui_thinking $thinking $time
                    set time {}
                }
                if {[dict exists $message content]} {
                    ui_assistant [dict get $message content] $time
                }
                set pending_tool_calls {}
                set pending_time $time
                if {[dict exists $message tool_calls]} {
                    set pending_tool_calls [dict get $message tool_calls]
                }
            }
            tool {
                set id [dict get $message tool_call_id]
                foreach call $pending_tool_calls {
                    if {[dict get $call id] eq $id} {
                        # The call belongs to the assistant message that made
                        # it, so it carries that message's time.
                        ui_tool_code $call $pending_time
                        break
                    }
                }
                ui_tool [dict get $message content] $time
            }
        }
    }
}


# --- the debug log ---
#
# What the harness did, in its own pane beneath the conversation: every
# request and response, every tool call and result. Long entries fold to
# their header and build their body the first time the marker opens them.

# JSON as it is read rather than as it is sent: jq formats it when jq is
# there, and it is shown as it came when it is not.
proc ::featherweight::pretty {raw} {
    if {[auto_execok jq] ne {}} {
        if {![catch {exec jq . << $raw} out]} {
            return $out
        }
    }
    return $raw
}


# One entry in the debug log: what the harness did, to the pane when there
# is one and to a file when there is not. Long entries fold to their header.
proc ::featherweight::debug_log {category text {label {}}} {
    variable config
    variable W
    variable debug_entry
    variable debug_open
    variable debug_text
    if {![dict get $config debug] && $category ni {error}} { return }
    set ts [format_time [clock seconds]]
    if {[dict exists $W debug]} {
        set w [dict get $W debug]
        $w configure -state normal
        if {$category in {request response} || $label ne {}} {
            # Something long: show the header and fold the body away the way
            # an HTML details element does. The label names the entry -- the
            # tool a call is for -- so the log stays navigable without
            # clicking. The marker opens it again.
            #
            # The body is not built here: a request or a tool result can be
            # a megabyte, formatting it with jq costs real time, and the
            # entry is folded shut anyway. What the entry holds is kept in
            # debug_text, and debug_toggle builds it the first time the
            # marker opens it. One empty line carries the body's tag so the
            # entry has a body to open even before there is text to show.
            incr debug_entry
            set n $debug_entry
            set debug_open($n) 0
            set debug_text($n) $text
            set head "$ts ($category)"
            if {$label ne {}} {
                append head " $label"
            }
            $w insert end "$head " [list cat_$category]
            $w insert end {[+]} [list cat_$category toggle$n]
            $w insert end "\n" [list cat_$category]
            $w insert end "\n" [list cat_$category body$n]
            $w tag configure body$n -elide 1
            # Tk draws an underline in the color of the tag that sets it, so
            # the marker's own tag carries the underline and its color.
            set color [debug_color $category]
            if {$color eq {}} {
                $w tag configure toggle$n -underline 1
            } else {
                $w tag configure toggle$n -underline 1 -underlinefg $color
            }
            $w tag bind toggle$n <Button-1> [list [namespace current]::debug_toggle $w $n]
        } else {
            $w insert end "$ts ($category) $text\n" [list "cat_$category"]
        }
        $w configure -state disabled
        $w see end
        return
    }
    # Trial fork: a headless turn logs to the state directory.
    variable log_dir
    set log_file [file join [pwd] featherweight-debug.log]
    if {[info exists log_dir] && $log_dir ne {}} {
        set log_file [file join $log_dir debug.log]
    }
    if {[catch {
        set fh [open $log_file a]
        ::puts $fh "$ts ($category) $text"
        close $fh
    }]} {}
}


# The color of one debug category, or {} for one that has none.
proc ::featherweight::debug_color {category} {
    variable debug_colors

    if {[dict exists $debug_colors $category]} {
        return [dict get $debug_colors $category]
    }
    return {}
}


# Open or close one folded debug entry. The marker is three characters wide
# either way, so nothing below it moves when it is clicked.
proc ::featherweight::debug_toggle {w n} {
    variable debug_open
    variable debug_text

    set open 0
    if {[info exists debug_open($n)]} {
        set open $debug_open($n)
    }
    $w configure -state normal
    if {$open} {
        $w tag configure body$n -elide 1
        set marker {[+]}
    } else {
        if {[info exists debug_text($n)]} {
            # The body was never built. It can be a megabyte and formatting
            # it for display costs real time, so it is built the first time
            # anyone asks for it: the entry's one empty line held its place.
            set ranges [$w tag ranges body$n]
            if {[llength $ranges] >= 2} {
                lassign $ranges start end
                $w insert $start [pretty $debug_text($n)] [$w tag names $start]
            }
            unset debug_text($n)
        }
        $w tag configure body$n -elide 0
        set marker "\[\u2212\]"
    }
    set ranges [$w tag ranges toggle$n]
    if {[llength $ranges] >= 2} {
        lassign $ranges start end
        set tags [$w tag names $start]
        $w delete $start $end
        $w insert $start $marker $tags
        $w see $start
    }
    set debug_open($n) [expr {!$open}]
    $w configure -state disabled
}


# What a file contains, in hex, which is what its stored copy is named after.
# --- the window and its commands ---
#
# The interface as its user sees it: the input and its Send button, the
# status line and what waits on it, images attached to the next message, and
# the slash commands that change the state between messages.

proc ::featherweight::attachment_digest {path} {
    return [::sha2::sha256 -hex -filename $path]
}


# Where a copy of each attached image is kept, so the record survives the
# original being moved or deleted.
proc ::featherweight::attachments_dir {} {
    return [file join [state_dir] attachments]
}


# What the images staged by /attach would add to the next request. A count
# of the conversation misses them until they are sent.
proc ::featherweight::attachments_tokens {} {
    variable attachments

    set total 0
    foreach attachment $attachments {
        incr total [image_tokens [dict get $attachment path]]
    }
    return $total
}


# Attach an image to the next user message. With no path the file dialog
# chooses one. The file is copied into the state directory.
proc ::featherweight::attach_image {{path {}}} {
    variable attachments

    if {$path eq {}} {
        set path [tk_getOpenFile -title {Attach an image} \
            -filetypes {{{Images} {.png .jpg .jpeg .gif .webp}} {{All files} *}}]
        if {$path eq {}} {
            return {}
        }
    }
    set path [file normalize $path]
    if {![file isfile $path]} {
        error "[list $path] is not a file"
    }

    set dir [attachments_dir]
    file mkdir $dir
    set name [file tail $path]
    # The blob is named by what it contains, so one picture is stored once
    # however often it is attached, and one name never means two things.
    set stored [file join $dir [attachment_digest $path]]
    if {![file exists $stored]} {
        file copy -force $path $stored
    }

    lappend attachments [dict create name $name path $stored]
    update_status_bar
    return [lindex $attachments end]
}


# Detach one attachment by number, or every one of them with no argument.
# The stored copy stays: blobs are content-addressed and shared between
# messages, so one that a message has already been sent with is still part of
# the record. Deleting them here is what left a sent message pointing at
# nothing and every later request failing.
proc ::featherweight::detach_attachments {{which {}}} {
    variable attachments

    if {$which eq {}} {
        set count [llength $attachments]
        set attachments {}
        update_status_bar
        return $count
    }
    if {![string is integer -strict $which]
            || $which < 1 || $which > [llength $attachments]} {
        error "[list $which] is not the number of an attachment"
    }
    set attachments [lreplace $attachments [expr {$which - 1}] [expr {$which - 1}]]
    update_status_bar
    return 1
}


# The status bar's text: the state, then any attachments waiting to go out
# with the next message -- Ready (1) screenshot.png (2) IMG_20261111.JPG
proc ::featherweight::status_text {text} {
    variable attachments

    set n 0
    foreach attachment $attachments {
        incr n
        append text " ($n) [dict get $attachment name]"
    }
    return $text
}


proc ::featherweight::update_status_bar {} {
    variable W
    variable busy

    if {![dict exists $W status]} { return }
    if {$busy} {
        [dict get $W status] configure -text [status_text {Working...}]
    } else {
        [dict get $W status] configure -text [status_text Ready]
    }
}


# The button and the status line say whether a turn is running: Send becomes
# Stop while one is.
proc ::featherweight::set_ui_busy {flag} {
    variable W
    variable busy
    set busy $flag
    if {![dict exists $W send]} { return }
    if {$flag} {
        [dict get $W send] configure -text "Stop" -state normal
        [dict get $W status] configure -text [status_text {Working...}]
    } else {
        [dict get $W send] configure -text "Send" -state normal
        [dict get $W status] configure -text [status_text Ready]
        focus [dict get $W entry]
    }
}


# Stop the turn in progress: the request being sent or retried, the tool
# call running, all of it.
proc ::featherweight::stop {} {
    variable busy
    variable curl_data
    variable request_channels
    variable retry_count
    variable run_code_token

    if {!$busy} { return }
    set busy 0
    set retry_count 0
    if {[info exists run_code_token] && $run_code_token ne {}} {
        run_code_expire $run_code_token stopped
    }
    foreach ch [array names request_channels] {
        fileevent $ch readable {}
        unset -nocomplain curl_data($ch)
        if {$::tcl_platform(platform) eq {windows}} {
            catch {exec taskkill /PID [pid $ch] /F}
        } else {
            catch {exec kill [pid $ch]}
        }
        catch {close $ch}
        unset request_channels($ch)
    }
    set_ui_busy 0
    debug_log tool "tool-call chain stopped by user"
}


proc ::featherweight::submit_or_stop {} {
    variable busy
    if {$busy} {
        stop
    } else {
        submit
    }
}


# Send what is in the input as the next user message, and start the turn.
proc ::featherweight::submit {} {
    variable W
    variable busy
    variable retry_count
    variable attachments
    if {$busy} { return }
    set entry [dict get $W entry]
    set text [string trim [$entry get 1.0 end-1c]]
    if {$text eq {}} { return }
    $entry delete 1.0 end
    if {[handle_slash_command $text]} { return }

    # Any attachments go out with this message, and are done with.
    set message [dict create role user]
    if {[llength $attachments]} {
        set content [list [dict create type text text $text]]
        foreach attachment $attachments {
            lappend content [dict create type image \
                name [dict get $attachment name] \
                path [dict get $attachment path]]
        }
        dict set message content $content
        set attachments {}
        update_status_bar
    } else {
        dict set message content $text
    }
    set message [append_message $message]
    ui_user [content_display [dict get $message content]] [message_time $message]
    set retry_count 0
    set_ui_busy 1
    request_async
}


# The title says where the harness is running and on what model. Both can move
# under us -- /eval can cd, /model changes the model, a tool can change either
# -- so it is rebuilt from state each time rather than kept.
proc ::featherweight::update_window_title {} {
    variable config
    variable W
    if {![dict exists $W chat]} { return }
    wm title . "Featherweight - [file tail [pwd]]/ - [dict get $config model]"
}


# Redraw the conversation from the session: after a fold, or after switching
# to another one.
proc ::featherweight::refresh_session {} {
    variable W
    if {![dict exists $W chat]} { return }
    set chat [dict get $W chat]
    $chat configure -state normal
    $chat delete 1.0 end
    $chat configure -state disabled
    replay_messages
    update_window_title
}


# Make another session the current one: load it, show it, save the choice.
proc ::featherweight::switch_session name {
    variable config
    variable sessions

    if {$name eq {}} {
        error {session name cannot be empty}
    }
    # Validates the name before anything is created or written.
    session_path $name

    # An existing session is loaded only now that it is about to be shown.
    if {[llength [load_session $name]] == 0} {
        dict set sessions $name [list \
            [dict create \
                role system \
                content [dict get $config system_prompt] \
                time [clock seconds] \
            ] \
        ]
        save_session $name
    }
    dict set config current_session $name
    save_config
    refresh_session
}


# Run a /command typed in the input, and say whether the text was one.
proc ::featherweight::handle_slash_command text {
    variable config
    variable W

    if {[string index $text 0] ne {/}} { return 0 }
    if {[regexp {^/([^[:space:]]+)(?:[[:space:]]+(.*))?$} $text -> command arguments]} {
        set command [string tolower $command]
    } else {
        return 0
    }

    switch -- $command {
        model {
            set model [string trim $arguments]
            if {$model eq {}} {
                debug_log tool "current model: [dict get $config model]"
                return 1
            }
            dict set config model $model
            save_config
            debug_log tool "model set to $model"
        }
        context-size {
            set value [string tolower [string trim $arguments]]
            if {$value eq {}} {
                debug_log tool [context_report]
                return 1
            }
            if {$value ni {off} && (![string is integer -strict $value] || $value < 4096)} {
                debug_log error {usage: /context-size ?tokens|off? (tokens must be at least 4096)}
                return 1
            }
            if {$value ne {off} && [llength $value] != 1} {
                debug_log error {usage: /context-size ?tokens|off? (tokens must be at least 4096)}
                return 1
            }
            if {$value eq {off}} {
                set value 0
            }
            dict set config context_size $value
            save_config
            debug_log tool [context_report]
        }
        compact {
            if {[string trim $arguments] ne {}} {
                debug_log error {usage: /compact}
                return 1
            }
            # Folds now, even when the history already fits the context budget.
            compact_context 1
        }
        eval {
            if {[string trim $arguments] eq {}} {
                debug_log error {usage: /eval Tcl-code}
                return 1
            }
            # The same output format as the run_code tool, but evaluated in
            # the harness's own thread: state from one /eval reaches the
            # next, unlike a tool call, which starts fresh each time.
            debug_log eval "/eval $arguments"
            debug_log eval [::run_code::execute $arguments]
        }
        session {
            set name [string trim $arguments]
            if {$name eq {}} {
                set names [session_names]
                debug_log tool "current session: [dict get $config current_session]; sessions: $names"
                return 1
            }
            if {[llength $name] != 1} {
                debug_log error {usage: /session ?name?}
                return 1
            }
            if {[catch {switch_session $name} error]} {
                debug_log error "could not switch session: $error"
                return 1
            }
            debug_log tool "switched to session $name"
        }
        attach {
            set path [string trim $arguments]
            if {[catch {attach_image $path} attachment]} {
                debug_log error "could not attach: $attachment"
                return 1
            }
            if {$attachment ne {}} {
                debug_log tool "attached [dict get $attachment name]"
            }
        }
        detach {
            set which [string trim $arguments]
            if {$which ne {} && ![string is integer -strict $which]} {
                debug_log error {usage: /detach ?number?}
                return 1
            }
            if {[catch {detach_attachments $which} count]} {
                debug_log error "could not detach: $count"
                return 1
            }
            set plural {s}
            if {$count == 1} {
                set plural {}
            }
            debug_log tool "detached $count attachment$plural"
        }
        default {
            debug_log error "unknown command: /$command"
        }
    }
    # Every command that got this far may have changed what the title shows:
    # /model the model, /eval the working directory, /session and /compact the
    # session. The queries above return early because they change nothing.
    update_window_title
    return 1
}


proc ::featherweight::on_close {} {
    variable busy
    variable done
    if {$busy} { stop }
    set done 1
}


proc ::featherweight::text_has_selection {w} {
    return [expr {[$w tag nextrange sel 1.0] ne {}}]
}


proc ::featherweight::text_copy w {
    if {![text_has_selection $w]} { return }
    clipboard clear -displayof $w
    clipboard append -displayof $w [$w get sel.first sel.last]
}


proc ::featherweight::text_cut w {
    if {![text_has_selection $w]} { return }
    text_copy $w
    $w delete sel.first sel.last
}


proc ::featherweight::text_paste w {
    if {[catch {clipboard get -displayof $w -type UTF8_STRING} text]} {
        if {[catch {clipboard get -displayof $w} text]} { return }
    }
    if {[text_has_selection $w]} {
        $w delete sel.first sel.last
    }
    $w insert insert $text
    $w see insert
}


proc ::featherweight::text_select_all w {
    $w tag remove sel 1.0 end
    $w tag add sel 1.0 end-1c
    $w mark set insert end-1c
    $w see insert
}


proc ::featherweight::show_text_context_menu {w x y editable} {
    variable W
    if {![dict exists $W context_menu]} {
        set menu .context_menu
        menu $menu -tearoff 0
        dict set W context_menu $menu
    } else {
        set menu [dict get $W context_menu]
    }
    $menu delete 0 end

    if {$editable} {
        $menu add command -label "Cut" -command [list [namespace current]::text_cut $w]
        $menu entryconfigure end -state [expr {[text_has_selection $w] ? "normal" : "disabled"}]
    }
    $menu add command -label "Copy" -command [list [namespace current]::text_copy $w]
    $menu entryconfigure end -state [expr {[text_has_selection $w] ? "normal" : "disabled"}]
    if {$editable} {
        $menu add command -label "Paste" -command [list [namespace current]::text_paste $w]
        if {[catch {clipboard get -displayof $w}]} {
            $menu entryconfigure end -state disabled
        } else {
            $menu entryconfigure end -state normal
        }
    }
    $menu add command -label "Select All" -command [list [namespace current]::text_select_all $w]
    $menu entryconfigure end -state [expr {[$w get 1.0 end-1c] eq {} ? "disabled" : "normal"}]

    $w mark set insert @$x,$y
    focus $w
    tk_popup $menu [expr {[winfo rootx $w] + $x}] [expr {[winfo rooty $w] + $y}]
}


# Give a text widget its right-click menu: cut and paste where the widget is
# editable, copy and select everywhere.
proc ::featherweight::install_text_context_menu {w {editable 0}} {
    set command [list [namespace current]::show_text_context_menu $w %x %y $editable]
    bind $w <Button-3> $command
    bind $w <Control-Button-1> $command
}


# The window: the conversation, the debug log beneath it, and the input with
# its Send button. Everything the interface can do is bound here.
proc ::featherweight::build_ui {} {
    variable W
    variable config

    catch {ttk::style theme use clam}

    wm minsize . 640 420
    wm geometry . 900x650
    wm protocol . WM_DELETE_WINDOW ::featherweight::on_close

    ttk::frame .root -padding {6 6 6 6}
    pack .root -fill both -expand 1

    ttk::panedwindow .root.pane -orient vertical
    pack .root.pane -fill both -expand 1

    ttk::frame .root.pane.chatf
    ttk::label .root.pane.chatf.lbl -text "Conversation" -anchor w
    pack .root.pane.chatf.lbl -side top -fill x
    ttk::frame .root.pane.chatf.body
    pack .root.pane.chatf.body -fill both -expand 1
    text .root.pane.chatf.body.t -wrap word -height 18 -width 80 -state disabled\
        -yscrollcommand {.root.pane.chatf.body.sb set}
    ttk::scrollbar .root.pane.chatf.body.sb -command {.root.pane.chatf.body.t yview}
    grid .root.pane.chatf.body.t .root.pane.chatf.body.sb -sticky nsew
    grid rowconfigure .root.pane.chatf.body 0 -weight 1
    grid columnconfigure .root.pane.chatf.body 0 -weight 1
    .root.pane add .root.pane.chatf -weight 3

    ttk::frame .root.pane.dbgf
    ttk::label .root.pane.dbgf.lbl -text "Debug" -anchor w
    pack .root.pane.dbgf.lbl -side top -fill x
    ttk::frame .root.pane.dbgf.body
    pack .root.pane.dbgf.body -fill both -expand 1
    text .root.pane.dbgf.body.t -wrap word -height 8 -width 80 -state disabled\
        -font TkFixedFont -background #11161c -foreground #d6d6d6\
        -yscrollcommand {.root.pane.dbgf.body.sb set}
    ttk::scrollbar .root.pane.dbgf.body.sb -command {.root.pane.dbgf.body.t yview}
    grid .root.pane.dbgf.body.t .root.pane.dbgf.body.sb -sticky nsew
    grid rowconfigure .root.pane.dbgf.body 0 -weight 1
    grid columnconfigure .root.pane.dbgf.body 0 -weight 1
    .root.pane add .root.pane.dbgf -weight 2

    # The input is the third pane below Conversation and Debug; the sash
    # above it sets its height, and padding keeps it clear of the sash.
    # Inside it, a horizontal panedwindow separates the text and Send button.
    # Weight 1 gives the text pane the space from window resizes; the button
    # stays at its requested width.
    ttk::frame .root.input -padding {0 6 0 0}
    ttk::panedwindow .root.input.p -orient horizontal
    text .root.input.p.e -wrap word -height 4 -width 40 -undo 1
    bind .root.input.p.e <Control-Return> {::featherweight::submit; break}
    bind .root.input.p.e <Control-KP_Enter> {::featherweight::submit; break}
    bind .root.input.p.e <Control-a> {::featherweight::text_select_all %W; break}
    ttk::button .root.input.p.send -text "Send" -command ::featherweight::submit_or_stop
    .root.input.p add .root.input.p.e -weight 1
    .root.input.p add .root.input.p.send -weight 0
    pack .root.input.p -fill both -expand 1
    .root.pane add .root.input -weight 1

    ttk::label .root.status -text "Ready" -anchor w
    pack .root.status -fill x

    set W [dict create\
        chat .root.pane.chatf.body.t\
        debug .root.pane.dbgf.body.t\
        entry .root.input.p.e\
        send .root.input.p.send\
        status .root.status]

    # update_window_title waits for the chat widget in W, so it can only run
    # once W is in place; called any earlier it would leave Tk's default title.
    update_window_title

    set chat [dict get $W chat]
    # Every font in the window at the configured size: the conversation, the
    # debug log, the labels, the input. role_font is made from TkTextFont
    # afterwards, so it comes out at the same size, in bold.
    set font_size [dict get $config font_size]
    font configure TkTextFont -size $font_size
    font configure TkDefaultFont -size $font_size
    font configure TkFixedFont -size $font_size
    font create ::featherweight::role_font  {*}[font configure TkTextFont]  -weight bold
    $chat tag configure role -font ::featherweight::role_font -foreground #555555
    # The time above each label is the size of the message text: it is the
    # label's weight that sets the two apart, so the time stays legible.
    $chat tag configure timestamp -font TkTextFont -foreground #555555
    $chat tag configure user -foreground #0b57d0
    $chat tag configure assistant -foreground #1b5e20
    $chat tag configure tool -foreground #6a1b9a
    # The model's reasoning is its work rather than its answer: shown faded out
    # and folded closed to begin with.
    $chat tag configure thinking -foreground #8a8a8a
    $chat configure -state disabled

    set dbg [dict get $W debug]
    variable debug_colors
    dict for {category color} $debug_colors {
        $dbg tag configure cat_$category -foreground $color
    }

    install_text_context_menu [dict get $W chat]
    install_text_context_menu [dict get $W debug]
    install_text_context_menu [dict get $W entry] 1
    focus [dict get $W entry]
}


# --- script mode ---
#
# The same state without a window: what a request would be, what a fold
# would cut, what the sessions hold. Nothing here sends a request; the
# replay runs one turn from recorded responses against a copied state.

proc ::featherweight::usage {} {
    return {Usage: tclsh featherweight.tcl [debug <subcommand> [options]]
       tclsh featherweight.tcl --help

With no arguments, the Tk interface starts.

  -m, --model MODEL      start on this model, as /model would
  -s, --session NAME     start on this session, as /session would

Script mode reads state and prints it. It never sends a request; loading
the state creates missing files the way starting the interface does, and
only debug eval can change what is stored:
  debug request [--session name] [--last n] [--replay file] [--keep]
      Print the request that would go out: model, endpoint, message
      history, tool schemas, approximate tokens. --last n lists the last
      n messages instead of printing the whole payload. --replay file
      runs one turn from recorded responses -- one response body per
      line -- against a throwaway copy of the state; the recorded tools
      still run for real.
  debug fold [--force]
      Show what a context fold would cut, without cutting it. --force is
      what /compact does.
  debug session list
  debug session show <name> [--last n]
  debug session check
      List the stored sessions, print one, or validate every session
      file and the configuration. check exits non-zero on a problem.
  debug eval <tcl-code>
      Evaluate Tcl as the run_code tool would and print what it printed
      and what it returned, in the harness's own thread. This one can
      change state.
  debug selftest
      Run the scripts in tests/. Exits non-zero if any of them fails.
  debug turn --message TEXT [--model MODEL] [--endpoint URL] [--provider NAME]
      Trial fork: run one live turn without the window -- the message is
      sent, tools run, and the turn continues until an answer without tool
      calls -- then print that answer. Exits 1 when the turn ended without
      one.}
}


proc ::featherweight::usage_error {message} {
    ::puts stderr $message
    ::puts stderr {}
    ::puts stderr [usage]
    exit 2
}


# One line describing a message, for the listings.
proc ::featherweight::message_summary {message} {
    if {[dict exists $message content] && [dict get $message content] ne {}} {
        set text [string map [list "\n" " "] [content_display [dict get $message content]]]
        if {[string length $text] > 120} {
            set text "[string range $text 0 119]..."
        }
        return $text
    }
    if {[dict exists $message tool_calls]} {
        set names {}
        foreach call [dict get $message tool_calls] {
            lappend names [dict get [dict get $call function] name]
        }
        return "tool calls: [join $names {, }]"
    }
    return {}
}


# Script mode: the command line the process started with, and the
# subcommand it names. Called instead of the interface when there are
# arguments; main decides which.
proc ::featherweight::script_main {argv} {
    variable json_tools
    variable tools_available

    set command [lindex $argv 0]
    if {$command in {-h --help help}} {
        ::puts stdout [usage]
        exit 0
    }
    if {$command ne {debug}} {
        usage_error "unknown option: $command"
    }

    set argv [lrange $argv 1 end]
    if {[llength $argv] == 0} {
        usage_error {debug needs a subcommand}
    }
    set sub [lindex $argv 0]
    set options [lrange $argv 1 end]

    # Trial fork: turn.
    if {$sub ni {request fold session eval selftest turn}} {
        usage_error "unknown subcommand: $sub"
    }

    # selftest needs no state; session reads it the way inspection should,
    # tolerating damage it has to report. The rest load it up front.
    if {$sub in {request fold eval turn}} {
        if {[catch {init_config} error]} {
            ::puts stderr "Error: $error"
            exit 1
        }
        set json_tools {}
        dict for {_name tool_args} $tools_available {
            lappend json_tools [json_tool {*}$tool_args]
        }
    }

    if {[catch {set code [script_$sub $options]} error]} {
        ::puts stderr "Error: $error"
        exit 1
    }
    exit $code
}


# What the request would be for a session, with no network: the model, the
# counts, the fold that would run first. With --replay the other half runs
# instead: one whole turn from recorded responses, against a copied state.
proc ::featherweight::script_request {options} {
    variable config
    variable json_tools

    set session {}
    set last 0
    set replay {}
    set keep 0
    while {[llength $options]} {
        set option [lindex $options 0]
        set options [lrange $options 1 end]
        switch -- $option {
            --session {
                if {[llength $options] == 0} { usage_error {--session needs a name} }
                set session [lindex $options 0]
                set options [lrange $options 1 end]
            }
            --last {
                if {[llength $options] == 0} { usage_error {--last needs a number} }
                set last [lindex $options 0]
                set options [lrange $options 1 end]
                if {![string is integer -strict $last] || $last < 1} {
                    usage_error {--last needs a positive number}
                }
            }
            --replay {
                if {[llength $options] == 0} { usage_error {--replay needs a file} }
                set replay [lindex $options 0]
                set options [lrange $options 1 end]
            }
            --keep { set keep 1 }
            default { usage_error "unknown option: $option" }
        }
    }

    if {$replay ne {}} {
        if {$last > 0} { usage_error {--last does not apply with --replay} }
        return [replay_turn $replay $session $keep]
    }

    # Look at another session without making it the current one. Nothing
    # here is saved, so the switch lives only in this process.
    set saved [dict get $config current_session]
    if {$session ne {}} {
        if {![session_exists $session]} {
            error "no such session: $session"
        }
        dict set config current_session $session
    }

    # The same question prepare_request asks -- would a fold run first? --
    # answered without writing anything.
    set plan [fold_plan]
    set messages [current_messages]
    set fold_line "automatic fold: would not run: [fold_decline $plan]"
    if {[dict get $plan fold]} {
        set messages [dict get $plan compacted]
        set fold_line "automatic fold: would cut at message [dict get $plan cut] of [dict get $plan before_count], [dict get $plan before] -> [dict get $plan after] tokens"
    }

    set size [dict get $config context_size]
    set history [messages_tokens $messages]
    set tools [tools_tokens $json_tools]
    set total [expr {$history + $tools}]

    ::puts stdout "session:  [current_session_name]"
    ::puts stdout "model:    [dict get $config model]"
    ::puts stdout "endpoint: [dict get $config endpoint]"
    ::puts stdout "history:  [llength $messages] messages, $history tokens"
    ::puts stdout "tools:    [llength $json_tools] schemas, $tools tokens"
    if {$size > 0} {
        ::puts stdout [format {context:  %d of %d tokens (%.1f%%); fold above %d history tokens} \
            $total $size [expr {100.0 * $total / $size}] [history_budget]]
    } else {
        ::puts stdout "context:  $total tokens; automatic folding is off"
    }
    ::puts stdout $fold_line
    dict set config current_session $saved

    if {$last > 0} {
        set count [llength $messages]
        set from [expr {max(0, $count - $last)}]
        ::puts stdout {}
        ::puts stdout "last [expr {$count - $from}] of $count messages:"
        for {set index $from} {$index < $count} {incr index} {
            set message [lindex $messages $index]
            ::puts stdout [format {%4d %-9s %s} $index \
                [dict get $message role] [message_summary $message]]
        }
        return 0
    }

    set payload [json_payload [dict get $config model] $json_tools $messages]
    ::puts stdout {}
    ::puts stdout $payload
    return 0
}


# Run one whole turn against a queue of recorded responses instead of the
# network. The state is copied first, so the real sessions are untouched; the
# tools the recorded turn calls still run for real, against the project.
proc ::featherweight::replay_turn {file session keep} {
    variable state_root

    set channel [open $file r]
    fconfigure $channel -encoding utf-8
    set responses {}
    while {[gets $channel line] >= 0} {
        set line [string trim $line]
        if {$line ne {}} { lappend responses $line }
    }
    close $channel
    if {[llength $responses] == 0} {
        error "replay file holds no responses: $file"
    }

    set handle [file tempfile path]
    close $handle
    file delete -force $path
    file mkdir $path
    set copy [file join $path featherweight]
    file copy -force [state_dir] $copy
    set state_root $copy

    if {[catch {init_config} error]} {
        file delete -force $path
        error "could not load the copied state: $error"
    }
    if {$session ne {}} {
        switch_session $session
    }

    ::puts stdout "replay: [llength $responses] recorded responses; state copied to $copy"
    ::puts stdout "replay: the recorded tools run for real against the project."

    set_ui_busy 1
    set requests 0
    set index 0
    while 1 {
        if {[catch {prepare_request} messages]} {
            set_ui_busy 0
            file delete -force $path
            error $messages
        }
        incr requests
        ::puts stdout "request $requests: [llength $messages] messages, [messages_tokens $messages] tokens"

        if {$index >= [llength $responses]} {
            ::puts stdout "replay: the queue ran out after $requests requests"
            break
        }
        set body [lindex $responses $index]
        incr index

        if {[catch {response_message $body} cleaned]} {
            set_ui_busy 0
            file delete -force $path
            error "response $index: $cleaned"
        }
        if {[dict exists $cleaned content]} {
            ::puts stdout "  assistant: [message_summary $cleaned]"
        }
        if {[dict exists $cleaned tool_calls]} {
            foreach call [dict get $cleaned tool_calls] {
                set function [dict get $call function]
                ::puts stdout "  tool call: [dict get $function name] [dict get $function arguments]"
            }
        }

        set before [llength [current_messages]]
        set more [apply_message $cleaned]
        set now [current_messages]
        for {set index2 $before} {$index2 < [llength $now]} {incr index2} {
            set message [lindex $now $index2]
            if {[dict get $message role] eq {tool}} {
                ::puts stdout "  result: [message_summary $message]"
            }
        }

        if {!$more} { break }
    }
    set_ui_busy 0

    set messages [current_messages]
    ::puts stdout "replay: turn over after $requests requests; [llength $messages] messages, [messages_tokens $messages] tokens"
    if {$keep} {
        ::puts stdout "replay: kept the copied state at $path"
    } else {
        file delete -force $path
    }
    return 0
}


# Trial fork: one live turn without Tk. It does what submit does for the
# window -- store the user message, mark the harness busy, send -- and then
# runs the event loop until the turn is over, the way the window's own loop
# would. The tool calls, retries and folds are the window's code.
proc ::featherweight::script_turn {options} {
    variable config
    variable busy
    variable retry_count
    variable log_dir

    set text {}
    while {[llength $options]} {
        set option [lindex $options 0]
        if {[llength $options] < 2} { usage_error "$option needs a value" }
        set value [lindex $options 1]
        set options [lrange $options 2 end]
        switch -- $option {
            --message { set text $value }
            --model { dict set config model $value }
            --endpoint { dict set config endpoint $value }
            --provider { dict set config provider_only [list $value] }
            default { usage_error "unknown option: $option" }
        }
    }
    if {[string trim $text] eq {}} {
        usage_error {turn needs --message}
    }
    set log_dir [state_dir]
    save_config

    set message [append_message [dict create role user content $text]]
    set start [llength [current_messages]]
    set retry_count 0
    set_ui_busy 1
    request_async
    while {$busy} {
        vwait ::featherweight::busy
    }

    # The turn's answer: its last assistant message, if that one called no
    # tools.
    set messages [current_messages]
    set steps 0
    set answer {}
    set ended 0
    foreach message [lrange $messages $start-1 end] {
        if {[dict get $message role] ne {assistant}} { continue }
        incr steps
        set ended [expr {![dict exists $message tool_calls]}]
        set answer {}
        if {[dict exists $message content]} {
            set answer [content_display [dict get $message content]]
        }
    }
    ::puts stdout "steps: $steps"
    if {!$ended || [string trim $answer] eq {}} {
        ::puts stdout "the turn ended without an answer; see [file join $log_dir debug.log]"
        return 1
    }
    ::puts stdout "answer:"
    ::puts stdout $answer
    return 0
}


# What a context fold would do, without doing it.
proc ::featherweight::script_fold {options} {
    variable config

    set force 0
    while {[llength $options]} {
        set option [lindex $options 0]
        set options [lrange $options 1 end]
        switch -- $option {
            --force { set force 1 }
            default { usage_error "unknown option: $option" }
        }
    }

    set kind {automatic fold}
    if {$force} {
        set kind {forced fold (/compact)}
    }

    set plan [fold_plan $force]
    set size [dict get $config context_size]
    ::puts stdout "session: [current_session_name] ([llength [current_messages]] messages, context_size $size)"
    if {[dict get $plan fold]} {
        set cut [dict get $plan cut]
        set before_count [dict get $plan before_count]
        ::puts stdout "$kind: would cut at message $cut of $before_count, dropping [expr {$cut - 1}] messages"
        ::puts stdout "  [dict get $plan before] -> [dict get $plan after] tokens, against a target of [dict get $plan target]"
        ::puts stdout "  the note would quote [llength [dict get $plan said]] user messages and start: [message_summary [dict get $plan note]]"
    } else {
        ::puts stdout "$kind: would not run: [fold_decline $plan]"
    }
    ::puts stdout {nothing is written; /compact in the interface applies the fold.}
    return 0
}


# Load the state the way starting the interface would, but keep going when it
# is damaged: session list and session check exist to report that damage.
proc ::featherweight::load_state_for_inspection {} {
    if {[catch {init_config} error]} {
        ::puts stderr "warning: could not load the state: $error"
        return $error
    }
    return {}
}


# The sessions on disk, one line each: which is current, how big, and which
# cannot be read.
proc ::featherweight::session_list {} {
    variable config

    set current {}
    if {[dict exists $config current_session]} {
        set current [dict get $config current_session]
    }

    set names [session_names]
    if {[llength $names] == 0} {
        ::puts stdout {no sessions yet}
        return
    }
    foreach name $names {
        set mark " "
        if {$name eq $current} { set mark "*" }
        set path [session_path $name]
        if {[catch {read_session $name} messages]} {
            ::puts stdout [format {%s %-20s %10d bytes  unreadable: %s} \
                $mark $name [file size $path] $messages]
            continue
        }
        ::puts stdout [format {%s %-20s %6d messages %8d tokens %10d bytes} \
            $mark $name [llength $messages] [messages_tokens $messages] [file size $path]]
    }
}


# One session printed: its messages in order, with their times and tool
# calls.
proc ::featherweight::session_show {name last} {
    if {![session_exists $name]} {
        error "no such session: $name"
    }
    set messages [load_session $name]

    set count [llength $messages]
    set from 0
    if {$last > 0} {
        set from [expr {max(0, $count - $last)}]
    }
    if {$from > 0} {
        ::puts stdout "$name ($count messages; showing the last $last)"
    } else {
        ::puts stdout "$name ($count messages)"
    }
    for {set index $from} {$index < $count} {incr index} {
        set message [lindex $messages $index]
        set header "$index [dict get $message role]"
        set time [message_time $message]
        if {$time ne {}} {
            append header " [format_time $time]"
        }
        ::puts stdout {}
        ::puts stdout $header
        if {[dict exists $message content]} {
            ::puts stdout [content_display [dict get $message content]]
        }
        if {[dict exists $message tool_calls]} {
            foreach call [dict get $message tool_calls] {
                set function [dict get $call function]
                ::puts stdout "tool call: [dict get $function name] [dict get $function arguments]"
            }
        }
    }
}


# Validate the configuration and every session file. Returns how many problems
# it found; nothing is written.
proc ::featherweight::session_check {} {
    variable config

    set problems 0

    set path [config_path]
    if {![file exists $path]} {
        ::puts stderr "config: $path does not exist"
        incr problems
    } elseif {[catch {
        dict size [::fileutil::cat -encoding utf-8 $path]
    } error]} {
        ::puts stderr "config: $path: $error"
        incr problems
    } else {
        ::puts stdout "config: ok ($path)"
    }

    foreach name [session_names] {
        set path [session_path $name]
        if {[catch {read_session $name} messages]} {
            ::puts stderr "session $name: $messages"
            incr problems
        } else {
            ::puts stdout "session $name: ok ([llength $messages] messages, [file size $path] bytes)"
        }
    }

    set current {}
    if {[dict exists $config current_session]} {
        set current [dict get $config current_session]
    }
    if {$current ne {} && ![session_exists $current]} {
        ::puts stderr "current session: $current has no file"
        incr problems
    }

    return $problems
}


# The session subcommands of script mode: list, show, check.
proc ::featherweight::script_session {options} {
    if {[llength $options] == 0} {
        usage_error {session needs list, show, or check}
    }
    set sub [lindex $options 0]
    set options [lrange $options 1 end]

    switch -- $sub {
        list {
            set damage [load_state_for_inspection]
            session_list
            if {$damage ne {}} { return 1 }
            return 0
        }
        show {
            if {[llength $options] == 0} {
                usage_error {session show needs a name}
            }
            set name [lindex $options 0]
            set options [lrange $options 1 end]
            set last 0
            while {[llength $options]} {
                set option [lindex $options 0]
                set options [lrange $options 1 end]
                switch -- $option {
                    --last {
                        if {[llength $options] == 0} { usage_error {--last needs a number} }
                        set last [lindex $options 0]
                        set options [lrange $options 1 end]
                        if {![string is integer -strict $last] || $last < 1} {
                            usage_error {--last needs a positive number}
                        }
                    }
                    default { usage_error "unknown option: $option" }
                }
            }
            session_show $name $last
            return 0
        }
        check {
            set damage [load_state_for_inspection]
            set problems 0
            if {$damage ne {}} { incr problems }
            incr problems [session_check]
            if {$problems} { return 1 }
            return 0
        }
        default {
            usage_error "unknown session subcommand: $sub"
        }
    }
}


# Evaluate Tcl in the ::run_code namespace, the way /eval and the run_code
# tool do, with the output on stdout instead of in the debug log.
proc ::featherweight::script_eval {options} {
    if {[llength $options] == 0} {
        usage_error {eval needs Tcl code}
    }
    set code [join $options { }]

    set ::run_code::output {}
    if {[catch {namespace eval ::run_code $code} value detail]} {
        ::puts stderr [dict get $detail -errorinfo]
        exit 1
    }
    if {$::run_code::output ne {} && ![string match *\n $::run_code::output]} {
        append ::run_code::output \n
    }
    ::puts -nonewline stdout $::run_code::output
    ::puts stdout $value
    return 0
}


# Run every script in tests/, each in its own tclsh so a failure cannot take
# this process down with it.
proc ::featherweight::script_selftest {options} {
    if {[llength $options]} {
        usage_error {selftest takes no options}
    }

    set directory [file join [file dirname [file normalize [info script]]] tests]
    if {![file isdirectory $directory]} {
        error "no tests directory at $directory"
    }
    set tclsh [auto_execok tclsh]
    if {$tclsh eq {}} {
        error {tclsh is not on PATH}
    }
    set guard {}
    if {[auto_execok timeout] ne {}} {
        set guard [auto_execok timeout]
    }

    set total 0
    set failed 0
    foreach path [lsort [glob -nocomplain -directory $directory *.tcl]] {
        if {[file tail $path] eq {lib.tcl}} { continue }
        incr total

        set cmdline [list {*}$tclsh $path]
        if {$guard ne {}} { set cmdline [list {*}$guard 120 {*}$cmdline] }
        lappend cmdline 2>@1
        set pipe [open |$cmdline]
        fconfigure $pipe -encoding utf-8
        set output [read $pipe]
        set code 0
        if {[catch {close $pipe} error detail]} {
            set errorcode [dict get $detail -errorcode]
            if {[lindex $errorcode 0] eq {CHILDSTATUS}} {
                set code [lindex $errorcode 2]
            } else {
                set code 1
                append output "$errorcode: $error"
            }
        }

        if {$code} {
            incr failed
            ::puts stdout "FAIL [file tail $path]"
            ::puts stdout $output
            continue
        }
        set checks {}
        regexp {all ([0-9]+) checks passed} $output -> checks
        if {$checks eq {}} { set checks {?} }
        ::puts stdout "pass  [file tail $path] ($checks checks)"
    }

    if {$total == 0} {
        error "no test scripts in $directory"
    }
    ::puts stdout {}
    if {$failed} {
        ::puts stdout "$failed of $total test scripts failed"
        return 1
    }
    ::puts stdout "all $total test scripts passed"
    return 0
}


# --- the entry point ---
#
# With arguments, script mode; without them, the window.

# The options the window starts with: -m/--model and -s/--session are the
# /model and /session its user would otherwise type once it is open. What
# remains is left alone: nothing, or the debug subcommand of script mode.
proc ::featherweight::startup_options {argv} {
    set options [dict create]
    while {[llength $argv] > 0} {
        set option [lindex $argv 0]
        switch -- $option {
            -m -
            --model {
                if {[llength $argv] < 2} {
                    usage_error "$option needs a model name"
                }
                dict set options model [lindex $argv 1]
            }
            -s -
            --session {
                if {[llength $argv] < 2} {
                    usage_error "$option needs a session name"
                }
                dict set options session [lindex $argv 1]
            }
            default {
                return [list $options $argv]
            }
        }
        set argv [lrange $argv 2 end]
    }
    return [list $options $argv]
}


# Do what /model and /session do: set the model or the session, and keep the
# choice for the next start.
proc ::featherweight::startup_configure {options} {
    variable config

    if {[dict exists $options model]} {
        dict set config model [dict get $options model]
        save_config
    }
    if {[dict exists $options session]} {
        if {[catch {switch_session [dict get $options session]} error]} {
            ::puts stderr "Error: $error"
            exit 1
        }
    }
}


proc ::featherweight::main {argv0 argv} {
    variable config
    variable done
    variable json_tools
    variable tools_available

    lassign [startup_options $argv] options argv
    if {[dict size $options] > 0} {
        init_config
        startup_configure $options
    }

    if {[llength $argv] > 0} {
        script_main $argv
        return
    }

    init_config

    if {[catch {package require Tk} err]} {
        ::puts stderr "Tk/display unavailable ($err)."
        ::puts stderr "For a headless run, use: [file tail $argv0] debug --help"
        exit 1
    }

    set json_tools {}
    dict for {_name tool_args} $tools_available {
        lappend json_tools [json_tool {*}$tool_args]
    }

    if {[catch {build_ui} err]} {
        ::puts stderr "Could not create UI ($err)."
        catch {destroy .}
        ::puts stderr "For a headless run, use: [file tail $argv0] debug --help"
        exit 1
    }

    replay_messages
    debug_log tool "model: [dict get $config model]"
    debug_log tool "endpoint: [dict get $config endpoint]"

    vwait ::featherweight::done
    catch {destroy .}
}


# If this is the main script...
if {[info exists argv0] && ([file tail [info script]] eq [file tail $argv0])} {
    ::featherweight::main $argv0 $argv
}
