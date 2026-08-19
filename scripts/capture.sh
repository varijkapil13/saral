#!/usr/bin/env bash
# Capture Jira API fixtures for pkg/jira/jiratest, scrubbed of anything identifying.
#
# Run by a human against a real site. Never in CI. Requires: curl, python3.
#
#   export SARAL_SITE=your-site.atlassian.net
#   export SARAL_EMAIL=you@example.com
#   export SARAL_TOKEN=...            # https://id.atlassian.com/manage-profile/security/api-tokens
#   export SARAL_PROJECT=PROJ         # a project you can read
#   export SARAL_ISSUE=PROJ-1         # an issue with a RICH description (lists, code, panels, links)
#   ./scripts/capture.sh
#
# Writes scrubbed JSON to pkg/jira/jiratest/fixtures/. Read the diff before committing:
# the scrubber is best-effort, and you are the last line of defence.
set -euo pipefail

: "${SARAL_SITE:?set SARAL_SITE}"
: "${SARAL_EMAIL:?set SARAL_EMAIL}"
: "${SARAL_TOKEN:?set SARAL_TOKEN}"
: "${SARAL_PROJECT:?set SARAL_PROJECT}"
: "${SARAL_ISSUE:?set SARAL_ISSUE}"

OUT="$(cd "$(dirname "$0")/.." && pwd)/pkg/jira/jiratest/fixtures"
mkdir -p "$OUT"

get() { # get <path> <fixture-name>
  local path="$1" name="$2" code
  code=$(curl -sS -u "$SARAL_EMAIL:$SARAL_TOKEN" -H 'Accept: application/json' \
    -o "$OUT/$name.raw" -w '%{http_code}' "https://$SARAL_SITE$path") || true
  scrub "$name" "$code" "GET $path"
}

post() { # post <path> <fixture-name> <json>
  local path="$1" name="$2" body="$3" code
  code=$(curl -sS -u "$SARAL_EMAIL:$SARAL_TOKEN" -X POST \
    -H 'Accept: application/json' -H 'Content-Type: application/json' -d "$body" \
    -o "$OUT/$name.raw" -w '%{http_code}' "https://$SARAL_SITE$path") || true
  scrub "$name" "$code" "POST $path"
}

scrub() {
  local name="$1" code="$2" what="$3"
  SARAL_SITE="$SARAL_SITE" SARAL_EMAIL="$SARAL_EMAIL" python3 - "$OUT/$name.raw" "$OUT/$name.json" <<'PY'
import json, os, re, sys, hashlib

src, dst = sys.argv[1], sys.argv[2]
raw = open(src, encoding="utf-8").read()
site, email = os.environ["SARAL_SITE"], os.environ["SARAL_EMAIL"]

def fake_id(v):
    return "acct" + hashlib.sha256(v.encode()).hexdigest()[:20]

SENSITIVE = {"emailAddress": "user@example.com", "displayName": "Test User",
             "name": None, "avatarUrls": "DROP", "timeZone": "Etc/UTC"}

def walk(o):
    if isinstance(o, dict):
        out = {}
        for k, v in o.items():
            if k == "avatarUrls":
                continue
            if k == "accountId" and isinstance(v, str):
                out[k] = fake_id(v); continue
            if k == "emailAddress":
                out[k] = "user@example.com"; continue
            if k == "displayName":
                out[k] = "Test User"; continue
            if k == "leadAccountId" and isinstance(v, str):
                out[k] = fake_id(v); continue
            out[k] = walk(v)
        return out
    if isinstance(o, list):
        return [walk(v) for v in o]
    if isinstance(o, str):
        s = o.replace(site, "example.atlassian.net").replace(email, "user@example.com")
        s = re.sub(r"[\w.+-]+@[\w-]+\.[\w.]+", "user@example.com", s)
        return s
    return o

try:
    data = walk(json.loads(raw))
except json.JSONDecodeError:
    sys.stderr.write(f"  !! {dst}: response was not JSON, not written\n")
    sys.exit(0)

json.dump(data, open(dst, "w", encoding="utf-8"), indent=2, ensure_ascii=False, sort_keys=True)
open(dst, "a").write("\n")
PY
  rm -f "$OUT/$name.raw"
  printf '  %-38s %s  %s\n' "$name" "$code" "$what"
}

echo "capturing from $SARAL_SITE -> $OUT"

# --- capability probe surface (P1.3) -----------------------------------------
get "/rest/api/3/myself" myself
get "/rest/api/3/configuration" configuration
get "/rest/api/3/mypermissions?permissions=BULK_CHANGE,MOVE_ISSUES,CREATE_ISSUES,DELETE_ISSUES,EDIT_ISSUES,DELETE_ALL_COMMENTS,ADMINISTER" mypermissions
get "/rest/api/3/plans?maxResults=5" plans          # expect 403 without Administer Jira — capture either way

# --- field and schema resolution (P2.2) --------------------------------------
get "/rest/api/3/field" fields
get "/rest/api/3/project/search?maxResults=50" projects
get "/rest/api/3/issue/createmeta?projectKeys=$SARAL_PROJECT&expand=projects.issuetypes.fields" createmeta

# --- issues, ADF and pagination (P1.2, P1.4) --------------------------------
get "/rest/api/3/issue/$SARAL_ISSUE" issue_rich
get "/rest/api/3/issue/$SARAL_ISSUE/transitions?expand=transitions.fields" transitions
get "/rest/api/3/issue/$SARAL_ISSUE/comment" comments
post "/rest/api/3/search/jql" search_page1 \
  "{\"jql\":\"project = $SARAL_PROJECT ORDER BY created DESC\",\"maxResults\":5,\"fields\":[\"summary\",\"status\",\"assignee\",\"issuetype\",\"priority\",\"updated\"]}"
post "/rest/api/3/search/jql/approximate-count" search_count \
  "{\"jql\":\"project = $SARAL_PROJECT\"}"

# --- versions (P5.1) ---------------------------------------------------------
get "/rest/api/3/project/$SARAL_PROJECT/version?maxResults=50" versions

# --- boards and sprints (P6.1, P6.2) ----------------------------------------
get "/rest/agile/1.0/board?projectKeyOrId=$SARAL_PROJECT" boards

cat <<'NOTE'

Done. Two follow-ups you must do by hand:

  1. search_page2 — take nextPageToken from search_page1.json and re-run the search with it,
     saving as search_page2.json. The paginator tests need a real second page.
  2. board_config / sprints — pick a board id from boards.json, then:
       ./scripts/capture.sh   # after: export SARAL_BOARD=<id>
     or capture directly:
       /rest/agile/1.0/board/<id>/configuration  -> board_config.json
       /rest/agile/1.0/board/<id>/sprint         -> sprints.json
     Capture one board WITH estimation and one with type "none" if you have both.

Fixtures that cannot be captured and are hand-authored instead:
  rate_limited.json (429 + Retry-After), bulkmove_task_*.json (each task state),
  validation_error.json (400 with per-field errors).

NOW READ THE DIFF. Check for real names, emails, account ids, and your site host before committing.
NOTE

if [ -n "${SARAL_BOARD:-}" ]; then
  get "/rest/agile/1.0/board/$SARAL_BOARD/configuration" board_config
  get "/rest/agile/1.0/board/$SARAL_BOARD/sprint?maxResults=10" sprints
  get "/rest/agile/1.0/board/$SARAL_BOARD/backlog?maxResults=5" backlog
fi
