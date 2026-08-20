#!/usr/bin/env python3
"""Pull one value out of a captured Jira response, for scripts/capture.sh.

Prints nothing and exits 0 when the file is missing, is not JSON, or does not carry the value, so
the caller can treat an empty string as "not there".
"""

import json
import sys


def load(path):
    try:
        with open(path, encoding="utf-8") as f:
            return json.load(f)
    except Exception:
        return None


def issue_types(doc):
    return doc.get("issueTypes", []) if isinstance(doc, dict) else []


def main() -> int:
    path, what = sys.argv[1], sys.argv[2]
    doc = load(path)
    if doc is None:
        return 0

    if what == "administers":
        perms = doc.get("permissions", {}) if isinstance(doc, dict) else {}
        if perms.get("ADMINISTER", {}).get("havePermission"):
            print("yes")
    elif what == "first-issue-type":
        for t in issue_types(doc):
            if not t.get("subtask"):
                print(t.get("id", ""))
                break
    elif what == "bug-issue-type":
        for t in issue_types(doc):
            if str(t.get("name", "")).lower() in ("bug", "defect"):
                print(t.get("id", ""))
                break
    elif what == "next-page-token":
        if isinstance(doc, dict) and doc.get("nextPageToken"):
            print(doc["nextPageToken"])
    elif what == "board-ids":
        values = doc.get("values", []) if isinstance(doc, dict) else []
        print(" ".join(str(b["id"]) for b in values[:8] if "id" in b))
    elif what == "account-name":
        if isinstance(doc, dict):
            print(doc.get("displayName") or doc.get("emailAddress") or doc.get("accountId", "?"))
    elif what == "project-keys":
        values = doc.get("values", []) if isinstance(doc, dict) else []
        for p in values:
            print(f"           {p.get('key','?'):<10} {p.get('name','')}")
    elif what == "estimation-type":
        estimation = (doc.get("estimation") or {}) if isinstance(doc, dict) else {}
        print(estimation.get("type", "none"))
    else:
        sys.stderr.write(f"pick: unknown value {what!r}\n")
        return 2
    return 0


if __name__ == "__main__":
    sys.exit(main())
