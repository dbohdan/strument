"""Builds the read-outline fixture: click and rich at pinned commits, vendored
side by side, with one planted change per task so that an answer from memory
of upstream is wrong. Each plant must match exactly as often as stated, or the
build fails rather than produce a fixture that answers differently.

Usage: fixture.py DEST
Environment: OUTLINE_SRC, a directory to cache the clones in.
"""
import os, shutil, subprocess, sys

REPOS = {
    "click": ("https://github.com/pallets/click", "06b2a67", "src/click"),
    "rich": ("https://github.com/Textualize/rich", "9d8f9a3", "rich"),
}

# (file, old, new, count). Two tasks' targets sit inside the read tool's
# 2,000-line default window (rich size, click abort); four sit past it.
PLANTS = [
    # T1 envvar: the auto envvar joins prefix and name with a double underscore.
    ("click/core.py", '{ctx.auto_envvar_prefix}_{self.name.upper()}', '{ctx.auto_envvar_prefix}__{self.name.upper()}', 3),
    # T2 metavar: a deprecated argument's metavar ends in "~".
    ("click/core.py", '            var += "!"\n', '            var += "~"\n', 1),
    # T3 flag: a flag with no flag_value delivers "on".
    ("click/core.py", "return True if self.is_flag else None", 'return "on" if self.is_flag else None', 1),
    ("click/core.py", "Resolves a missing :attr:`flag_value` to ``True``", 'Resolves a missing :attr:`flag_value` to ``"on"``', 1),
    # T4 pipe: a broken pipe exits with status 3.
    ("rich/console.py", "raise SystemExit(1)", "raise SystemExit(3)", 1),
    # T5 dumb (control, inside the window): a dumb terminal is 72x21.
    ("rich/console.py", "return ConsoleDimensions(80, 25)", "return ConsoleDimensions(72, 21)", 1),
    # T6 abort (control, inside the window): the abort message.
    ("click/core.py", 'echo(_("Aborted!"), file=sys.stderr)', 'echo(_("Stopped by user."), file=sys.stderr)', 1),
    # T7 exports (structural, past the window): Console gains a Markdown pair.
    ("rich/console.py", "    def export_html(\n",
     "    def export_markdown(self, *, clear: bool = True) -> str:\n"
     '        """Export console contents as a fenced Markdown block."""\n'
     '        return "```\\n" + self.export_text(clear=clear) + "\\n```\\n"\n\n'
     "    def save_markdown(self, path: str, *, clear: bool = True) -> None:\n"
     '        """Save console contents to a file as a fenced Markdown block."""\n'
     '        with open(path, "w", encoding="utf-8") as write_file:\n'
     "            write_file.write(self.export_markdown(clear=clear))\n\n"
     "    def export_html(\n", 1),
    # T8 argmethods (structural, past the window): Argument gains describe().
    ("click/core.py",
     "    def add_to_parser(self, parser: _OptionParser, ctx: Context) -> None:\n"
     "        parser.add_argument(dest=self.name, nargs=self.nargs, obj=self)\n",
     "    def add_to_parser(self, parser: _OptionParser, ctx: Context) -> None:\n"
     "        parser.add_argument(dest=self.name, nargs=self.nargs, obj=self)\n\n"
     "    def describe(self) -> str:\n"
     '        """One line naming the argument and how many values it takes."""\n'
     '        return f"{self.human_readable_name} ({self.nargs})"\n', 1),
]


def source(name):
    url, rev, sub = REPOS[name]
    cache = os.path.join(os.environ["OUTLINE_SRC"], name)
    if not os.path.isdir(cache):
        subprocess.run(["git", "clone", "-q", url, cache], check=True)
    head = subprocess.run(["git", "-C", cache, "rev-parse", "--short=7", "HEAD"],
                          check=True, capture_output=True, text=True).stdout.strip()
    if head != rev:
        subprocess.run(["git", "-C", cache, "fetch", "-q", "--unshallow"], capture_output=True)
        subprocess.run(["git", "-C", cache, "checkout", "-q", rev], check=True)
    return os.path.join(cache, sub)


def main():
    dest = sys.argv[1]
    os.makedirs(dest)
    for name in REPOS:
        shutil.copytree(source(name), os.path.join(dest, name),
                        ignore=shutil.ignore_patterns("__pycache__", "*.pyc"))
    for rel, old, new, count in PLANTS:
        path = os.path.join(dest, rel)
        text = open(path).read()
        if text.count(old) != count:
            sys.exit(f"{rel}: {old!r} occurs {text.count(old)} times, want {count}")
        open(path, "w").write(text.replace(old, new))
    open(os.path.join(dest, "README.md"), "w").write(
        "# vendored\n\nclick and rich, vendored with local changes.\n")


if __name__ == "__main__":
    main()
