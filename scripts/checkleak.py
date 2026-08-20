#!/usr/bin/env python3
"""Prove that nothing from a real Jira capture reached the committed fixtures.

    scripts/checkleak.py [live-dir] [fixture-dir]

Captures live outside the tracked tree because they carry the words a company
wrote — ticket summaries, release names, custom field names, board and plan
names. Fixtures are corrected from a capture's *shape* and given invented words.
This checks that the second half actually happened.

It compares the distinctive strings of the two trees and ignores the vocabulary
Jira ships identically on every site, which the fixtures are supposed to share.
Exits non-zero if anything company-specific appears in both.
"""

import json
import os
import re
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
    "stories track functionality or features expressed as user goals.",
    "a big user story that needs to be broken down. created by jira software - do not edit or delete.",
    "the sub-task of the issue",
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
    if text in STOCK or text.startswith(STOCK_PREFIXES):
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


def load(directory):
    found = {}
    for name in sorted(os.listdir(directory)):
        if not name.endswith(".json"):
            continue
        try:
            found[name] = strings(os.path.join(directory, name))
        except (json.JSONDecodeError, UnicodeDecodeError):
            continue
    return found


def main() -> int:
    root = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
    live_dir = sys.argv[1] if len(sys.argv) > 1 else f"{root}/testdata/live/fixtures"
    fix_dir = sys.argv[2] if len(sys.argv) > 2 else f"{root}/pkg/jira/jiratest/fixtures"

    if not os.path.isdir(live_dir):
        print(f"no capture at {live_dir}; nothing to compare against")
        return 0

    live_words = set()
    for values in load(live_dir).values():
        for value in values:
            live_words |= distinctive(value)

    hits = []
    for name, values in load(fix_dir).items():
        for value in values:
            shared = distinctive(value) & live_words
            if shared:
                hits.append((name, value, sorted(shared)[:6]))

    if not hits:
        print(f"clean: no distinctive string from {live_dir} appears in {fix_dir}")
        return 0

    print(f"{len(hits)} string(s) in the committed fixtures also appear in your capture:\n")
    for name, value, shared in hits:
        print(f"  {name}")
        print(f"    {value!r}")
        print(f"    shares: {', '.join(shared)}")
    print("\nEvery one of these is either a word Jira ships on every site — add it to STOCK —")
    print("or something your company wrote, which must not be committed.")
    return 1


if __name__ == "__main__":
    sys.exit(main())
