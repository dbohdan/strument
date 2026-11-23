"""Count, per session, edits made through the edit and write tools and shell
commands that write files, and whether the tests pass and the README typo is
fixed. Usage: python3 score.py RUNS_DIR"""
import glob, os, re, sys

ANSI = re.compile(r"\x1b\[[0-9;?]*[A-Za-z]")
# A command that writes a file: a redirect into a heredoc or a .go/.md file,
# an in-place sed or perl, tee, or a python script fed on stdin.
WRITES = re.compile(r"(cat\s*>|>\s*[\w./-]+\.(go|md)\b|sed\s+-i|perl\s+-[pi]|tee\s|python3?\s+-\s*<<)")
MENTION = re.compile(r"edit tool|use edit|write tool|edit/write|edit or write", re.I)

for d in sorted(glob.glob(os.path.join(sys.argv[1], "*"))):
    out = ANSI.sub("", open(os.path.join(d, "out.txt"), errors="replace").read())
    tool = len(re.findall(r"^Applied (?:edit|write)", out, re.M))
    shell = [c for c in re.findall(r"^\$ (.*)", out, re.M) if WRITES.search(c)]
    thinking = "\n".join(re.findall(r"^‹thinking›.*", out, re.M))
    tests = "test-exit=0" in open(os.path.join(d, "test.txt")).read()
    readme = "arithmetic" in open(os.path.join(d, "proj", "README.md")).read()
    print(f"{os.path.basename(d):16} tool_edits={tool} shell_writes={len(shell)} "
          f"mentions_tools={'yes' if MENTION.search(thinking) else 'no'} "
          f"tests={'ok' if tests else 'FAIL'} readme={'ok' if readme else 'no'}")
