#!/usr/bin/env python3
"""Scrub a captured Jira response so it is safe to commit as a fixture.

Reads one JSON file and writes a scrubbed copy. Exits non-zero without writing if the input is not
JSON, so the caller can keep the raw body for inspection.

It handles structure: account ids, display names, email addresses, avatar URLs, the site host, and
the names inside ADF mention nodes. It cannot handle prose — a summary or a comment body that names
a customer stays exactly as it was, which is why the capture script tells you to read the diff.
"""

import hashlib
import json
import os
import re
import sys

EMAIL = re.compile(r"[\w.+-]+@[\w-]+\.[\w.]+")

# Anything that identifies a person, mapped to a stable stand-in so that two comments by the same
# author still look like the same author in the fixture.
ACCOUNT_KEYS = {"accountId", "leadAccountId", "authorAccountId", "updateAuthorAccountId"}
DROP_KEYS = {"avatarUrls", "profilePicture"}


def fake_account(value: str) -> str:
    return "5b10a2844c20165700" + hashlib.sha256(value.encode()).hexdigest()[:6]


def fake_name(value: str) -> str:
    return "User " + hashlib.sha256(value.encode()).hexdigest()[:4].upper()


def scrub(node, site: str, email: str):
    if isinstance(node, dict):
        # An ADF mention carries the person's real name in attrs.text, which no generic rule catches.
        if node.get("type") == "mention" and isinstance(node.get("attrs"), dict):
            attrs = dict(node["attrs"])
            if isinstance(attrs.get("id"), str):
                attrs["id"] = fake_account(attrs["id"])
            if isinstance(attrs.get("text"), str) and attrs["text"].startswith("@"):
                attrs["text"] = "@" + fake_name(attrs["text"][1:])
            node = {**node, "attrs": attrs}

        out = {}
        for key, value in node.items():
            if key in DROP_KEYS:
                continue
            if key in ACCOUNT_KEYS and isinstance(value, str):
                out[key] = fake_account(value)
            elif key == "emailAddress":
                out[key] = "user@example.com"
            elif key == "displayName" and isinstance(value, str):
                out[key] = fake_name(value)
            else:
                out[key] = scrub(value, site, email)
        return out

    if isinstance(node, list):
        return [scrub(v, site, email) for v in node]

    if isinstance(node, str):
        text = node.replace(site, "example.atlassian.net").replace(email, "user@example.com")
        return EMAIL.sub("user@example.com", text)

    return node


def main() -> int:
    src, dst = sys.argv[1], sys.argv[2]
    site, email = os.environ["SARAL_SITE"], os.environ["SARAL_EMAIL"]
    try:
        with open(src, encoding="utf-8") as f:
            data = json.load(f)
    except (json.JSONDecodeError, UnicodeDecodeError):
        return 1

    # Key order is preserved on purpose: the point of a captured fixture is that it looks like what
    # the wire actually sent, and pkg/adf's round-trip guarantee is about exact bytes.
    with open(dst, "w", encoding="utf-8") as f:
        json.dump(scrub(data, site, email), f, indent=2, ensure_ascii=False)
        f.write("\n")
    return 0


if __name__ == "__main__":
    sys.exit(main())
