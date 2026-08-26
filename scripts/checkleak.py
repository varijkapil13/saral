#!/usr/bin/env python3
"""Prove that nothing from a real Jira capture reached the committed fixtures.

    scripts/checkleak.py [live-dir] [fixture-dir] [--require-capture]

Captures live outside the tracked tree because they carry the words a company
wrote — ticket summaries, release names, custom field names, board and plan
names. Fixtures are corrected from a capture's *shape* and given invented words.
This checks that the second half actually happened.

It runs in two halves and says which of them ran.

The first needs no capture, so CI runs it on every pull request: the fixture tree
has to hold on its own — it is not empty, every file in it parses, and every
absolute URL names the invented site — and no capture may be tracked by git or
have lost the ignore rule that keeps it untracked.

The second compares the distinctive strings of the two trees, ignoring the
vocabulary Jira ships identically on every site. It only runs where a capture is,
which is the machine that ran scripts/capture.sh and never a CI runner. Pass
--require-capture to make its absence an error rather than a skip.

Exits non-zero if either half finds something.
"""

import argparse
import json
import os
import re
import subprocess
import sys

# Vocabulary every Jira site has. Sharing these is the point, not a leak.
STOCK = {
    # status categories and their display names
    "new", "indeterminate", "done", "undefined", "to do", "in progress",
    "blue-gray", "yellow", "green", "medium-gray", "no category",
    # permissions and Jira's own descriptions of them
    "administer jira", "bulk change", "create issues", "delete issues",
    "edit issues", "move issues", "add comments", "transition issues",
    "create attachments", "delete all comments", "make bulk changes",
    # stock issue types and their descriptions
    "task", "bug", "story", "epic", "sub-task", "subtask",
    "a task that needs to be done.",
    "a problem which impairs or prevents the functions of the product.",
    "a problem or error.",
    "stories track functionality or features expressed as user goals.",
    "a big user story that needs to be broken down. created by jira software - do not edit or delete.",
    "the sub-task of the issue",
    # validation messages Jira writes itself
    "you must specify a summary of the issue.",
    # system field names
    "summary", "description", "assignee", "reporter", "creator", "priority",
    "labels", "components", "fix versions", "affects versions", "due date",
    "created", "updated", "resolution", "status", "issue type", "project",
    "attachment", "comment", "worklog", "time tracking", "linked issues",
    "story points", "parent", "key", "watchers", "votes", "environment",
    # priorities and resolutions Jira ships
    "highest", "high", "low", "lowest", "won't do", "duplicate", "cannot reproduce",
    "resolved", "target start", "target end", "start date", "rank", "sprint",
    "development", "request type", "approvals", "time to resolution",
    "steps to reproduce", "acceptance criteria", "definition of done",
}

# HTTP reason phrases, which an RFC 7807 problem document repeats verbatim in its
# `title`. They come from the specification rather than from a site, so a capture
# and a fixture answering the same status say the same word by construction.
#
# These are matched only against a whole value, never against a word inside one:
# "Forbidden" alone is the protocol's word for 403, but "forbidden" in the middle
# of a sentence is somebody's prose and has to stay catchable.
STOCK_EXACT = {
    "bad request", "unauthorized", "payment required", "forbidden", "not found",
    "method not allowed", "not acceptable", "proxy authentication required",
    "request timeout", "conflict", "gone", "length required",
    "precondition failed", "content too large", "payload too large",
    "uri too long", "unsupported media type", "range not satisfiable",
    "expectation failed", "misdirected request", "unprocessable content",
    "unprocessable entity", "too many requests", "request header fields too large",
    "internal server error", "not implemented", "bad gateway",
    "service unavailable", "gateway timeout", "http version not supported",
}

# Jira writes every permission description as "Ability to ...", so the prefix is
# a reliable marker for its own boilerplate rather than anyone's prose.
STOCK_PREFIXES = ("ability to ", "users with this permission ", "modify collections of ",
                  "create and administer ", "this work item is ", "dieser status wird ")

# Words that recur in anything Jira-shaped, invented or not. A match on one of
# these says nothing; the list is the difference between a check people run and
# a check people ignore.
COMMON = {
    "about", "above", "access", "action", "active", "actual", "added", "after",
    "against", "agile", "allowed", "another", "atlassian", "avatar", "backlog",
    "before", "being", "board", "browse", "cannot", "change", "changes", "check",
    "child", "cloud", "column", "columns", "comment", "comments", "common",
    "complete", "component", "components", "config", "configuration", "content",
    "current", "custom", "customfield", "default", "delete", "deploy", "detail",
    "details", "display", "document", "epic", "error", "estimate", "estimation",
    "example", "existing", "expand", "field", "fields", "filter", "first",
    "fixed", "following", "format", "from", "future", "group", "groups", "have",
    "hotfix", "icon", "image", "import", "index", "internal", "issue", "issues",
    "items", "jira", "kanban", "level", "linked", "links", "location", "manage",
    "managed", "media", "member", "message", "migrate", "multi", "name", "names",
    "needs", "night", "notification", "number", "object", "only", "open",
    "operations", "order", "original", "other", "over", "page", "panel",
    "parent", "permission", "permissions", "plan", "plans", "point", "points",
    "product", "progress", "project", "projects", "provision", "query", "ready",
    "realm", "record", "release", "released", "remaining", "report", "request",
    "required", "resolution", "restore", "review", "role", "same", "schema",
    "scope", "screen", "scrum", "search", "self", "service", "shared", "should",
    "small", "software", "some", "source", "sources", "sprint", "sprints",
    "staging", "start", "state", "status", "step", "stories", "story", "sub",
    "system", "table", "task", "tasks", "team", "test", "text", "that", "their",
    "them", "then", "there", "this", "time", "title", "tracking", "type",
    "types", "until", "update", "updated", "used", "user", "users", "value",
    "values", "version", "versions", "view", "waiting", "when", "which", "with",
    "work", "would", "write", "migration", "customer", "reproduce", "collections",
    "workflows", "behind", "saved", "steps",
}

WORD = re.compile(r"[A-Za-z][A-Za-z0-9_-]{3,}")
TEXT_KEYS = {"name", "summary", "description", "value", "text", "title", "goal",
             "untranslatedName", "displayName", "label"}

# The invented site, and the hosts a fixture may name beside it. A captured
# response reaches for a great many others — an avatar CDN, api.media.atlassian.com,
# whatever a description links to on the company's own intranet — and none of
# them survives being pasted into a fixture.
ALLOWED_HOSTS = {"example.atlassian.net", "example.com", "www.example.com",
                 "localhost", "127.0.0.1"}

URL_HOST = re.compile(r"[a-zA-Z][a-zA-Z0-9+.-]*://([^/\s\"'\\<>]+)")

CAPTURE_DIR = "testdata/live"


def strings(path):
    """Every human-written string in a JSON tree, by the key that held it."""
    out = set()

    def walk(node):
        if isinstance(node, dict):
            for key, value in node.items():
                if key in TEXT_KEYS and isinstance(value, str) and value.strip():
                    out.add(value.strip())
                walk(value)
        elif isinstance(node, list):
            for value in node:
                walk(value)

    with open(path, encoding="utf-8") as f:
        walk(json.load(f))
    return out


def distinctive(value):
    """The parts of a string that could only have come from one instance.

    A shared word like "board" or "release" means nothing — invented content uses
    those too. What gives a capture away is the whole phrase repeated verbatim, a
    version or release number, an identifier in caps or snake_case, or an unusual
    long word.
    """
    text = " ".join(value.split()).lower()
    if text in STOCK or text in STOCK_EXACT or text.startswith(STOCK_PREFIXES):
        return set()

    out = set()
    if len(text) > 8:
        out.add(f"phrase:{text}")

    for word in WORD.findall(value):
        low = word.lower()
        if low in STOCK or low in COMMON:
            continue
        distinctive_shape = (
            any(c.isdigit() for c in word)
            or "_" in word
            or (word.isupper() and len(word) >= 3)
            or len(word) >= 8
        )
        if distinctive_shape:
            out.add(low)
    return out


def fixtures(directory):
    """Every .json file under directory, deepest paths included."""
    out = []
    for parent, dirs, names in os.walk(directory):
        dirs.sort()
        for name in sorted(names):
            if name.endswith(".json"):
                out.append(os.path.join(parent, name))
    return out


def load(directory):
    found = {}
    for path in fixtures(directory):
        try:
            found[os.path.relpath(path, directory)] = strings(path)
        except (json.JSONDecodeError, UnicodeDecodeError):
            continue
    return found


def hosts(text):
    """Every host an absolute URL in text points at, without userinfo or port."""
    out = []
    for authority in URL_HOST.findall(text):
        host = authority.rsplit("@", 1)[-1]
        if host.startswith("["):
            host = host.partition("]")[0] + "]"
        else:
            host = host.partition(":")[0]
        if host:
            out.append(host.lower())
    return out


def standalone(fix_dir):
    """What the fixture tree has to satisfy with no capture to compare against.

    The email, credential and account-id shapes are asserted next door, over the
    same tree, by pkg/jira/jiratest/fixtures_test.go, and are deliberately not
    repeated here.
    """
    if not os.path.isdir(fix_dir):
        return [f"{fix_dir} is not a directory, so this check would pass by reading nothing"]

    found = fixtures(fix_dir)
    if not found:
        return [f"no .json fixture under {fix_dir}, so this check would pass by reading nothing"]

    problems = []
    for path in found:
        name = os.path.relpath(path, fix_dir)
        try:
            with open(path, encoding="utf-8") as f:
                text = f.read()
            json.loads(text)
        except (OSError, json.JSONDecodeError, UnicodeDecodeError) as err:
            problems.append(f"{name}: the leak check cannot read this fixture: {err}")
            continue
        for host in hosts(text):
            if host not in ALLOWED_HOSTS:
                problems.append(f"{name}: names the host {host}, which is not the invented site")
    return problems


def git(root, *args):
    try:
        return subprocess.run(["git", "-C", root, *args],
                              capture_output=True, text=True, check=False)
    except OSError:
        return None


def untracked_capture(root):
    """A capture must stay outside the tracked tree, and stay ignored.

    Deleting the ignore rule is the quiet way this goes wrong: nothing breaks
    until the next `git add -A` after a capture, and by then it is in the history.
    """
    inside = git(root, "rev-parse", "--is-inside-work-tree")
    if inside is None or inside.returncode != 0:
        return [], f"skipped: {root} is not a git checkout"

    problems = []
    tracked = git(root, "ls-files", "--", CAPTURE_DIR)
    for name in tracked.stdout.split("\n"):
        if name.strip():
            problems.append(f"{name.strip()} is tracked by git; a capture is never committed")

    probe = f"{CAPTURE_DIR}/fixtures/probe.json"
    if git(root, "check-ignore", "-q", probe).returncode != 0:
        problems.append(f"{CAPTURE_DIR}/ is no longer ignored, so the next `git add -A` "
                        f"after a capture commits it")
    return problems, f"{CAPTURE_DIR}/ is ignored and holds nothing tracked"


def differential(live_dir, fix_dir):
    live_words = set()
    for values in load(live_dir).values():
        for value in values:
            live_words |= distinctive(value)

    hits = []
    for name, values in sorted(load(fix_dir).items()):
        for value in sorted(values):
            shared = distinctive(value) & live_words
            if shared:
                hits.append((name, value, sorted(shared)[:6]))
    return hits


def parse(argv):
    parser = argparse.ArgumentParser(description=__doc__.split("\n", maxsplit=1)[0])
    parser.add_argument("live_dir", nargs="?")
    parser.add_argument("fix_dir", nargs="?")
    parser.add_argument("--require-capture", action="store_true",
                        help="fail if there is no capture to compare the fixtures against")
    return parser.parse_args(argv)


def main(argv=None) -> int:
    args = parse(argv)
    root = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
    live_dir = args.live_dir or f"{root}/{CAPTURE_DIR}/fixtures"
    fix_dir = args.fix_dir or f"{root}/pkg/jira/jiratest/fixtures"

    failed = False

    print(f"the fixture tree on its own: {fix_dir}")
    problems = standalone(fix_dir)
    for problem in problems:
        print(f"  {problem}")
    if problems:
        failed = True
    else:
        print(f"  clean: {len(fixtures(fix_dir))} fixtures, every URL on the invented site")

    print("no capture in the tracked tree:")
    problems, note = untracked_capture(root)
    for problem in problems:
        print(f"  {problem}")
    print(f"  {note}")
    if problems:
        failed = True

    print(f"the fixtures against your capture: {live_dir}")
    if not os.path.isdir(live_dir):
        print("  skipped: no capture here, so nothing was compared. Only the machine that ran")
        print("  scripts/capture.sh can run this half; CI has no capture and never will.")
        if args.require_capture:
            print("  --require-capture was given and there is nothing to compare against")
            failed = True
    else:
        hits = differential(live_dir, fix_dir)
        for name, value, shared in hits:
            print(f"  {name}")
            print(f"    {value!r}")
            print(f"    shares: {', '.join(shared)}")
        if hits:
            print(f"  {len(hits)} string(s) in the committed fixtures also appear in your capture.")
            print("  Every one is either a word Jira ships on every site — add it to STOCK —")
            print("  or something your company wrote, which must not be committed.")
            failed = True
        else:
            print("  clean: no distinctive string from the capture appears in the fixtures")

    return 1 if failed else 0


if __name__ == "__main__":
    sys.exit(main())
