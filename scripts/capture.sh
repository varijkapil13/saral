#!/usr/bin/env bash
# Capture Jira API fixtures for pkg/jira/jiratest, scrubbed of anything identifying.
#
# Run by a human against a real site. Never in CI. Requires: curl, python3.
#
#   ./scripts/capture.sh
#
# It asks for what it needs. Set any of these first to skip the prompt:
#
#   SARAL_SITE=your-site.atlassian.net
#   SARAL_EMAIL=you@example.com
#   SARAL_TOKEN=...          # https://id.atlassian.com/manage-profile/security/api-tokens
#   SARAL_PROJECT=PROJ       # a project you can read
#   SARAL_ISSUE=PROJ-1       # an issue with a RICH description: lists, code, panels, links, an image
#
# Writes scrubbed JSON to pkg/jira/jiratest/fixtures/, replacing what is there. Read the diff before
# committing: the scrubber is best-effort and you are the last line of defence.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
OUT="$ROOT/pkg/jira/jiratest/fixtures"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
mkdir -p "$OUT"

for tool in curl python3; do
  command -v "$tool" >/dev/null || { echo "capture: $tool is required" >&2; exit 1; }
done

ask() { # ask <var> <prompt> [secret]
  local var="$1" prompt="$2" secret="${3:-}" value="${!1:-}"
  if [ -n "$value" ]; then return; fi
  if [ ! -t 0 ]; then
    echo "capture: $var is not set and there is no terminal to ask on; export it and re-run" >&2
    exit 1
  fi
  if [ -n "$secret" ]; then
    read -rsp "$prompt: " value; echo
  else
    read -rp "$prompt: " value
  fi
  [ -n "$value" ] || { echo "capture: $var cannot be empty" >&2; exit 1; }
  printf -v "$var" '%s' "$value"
  export "${var?}"
}

ask SARAL_SITE    "Jira site (host only, e.g. example.atlassian.net)"
ask SARAL_EMAIL   "Atlassian account email"
ask SARAL_TOKEN   "API token (not echoed)" secret
ask SARAL_PROJECT "Project key to capture (e.g. PROJ)"
ask SARAL_ISSUE   "Issue key with a rich description (e.g. PROJ-1)"

SARAL_SITE="${SARAL_SITE#https://}"
SARAL_SITE="${SARAL_SITE%/}"

# The token goes through a curl config on a pipe, never through argv, so it does not show up in ps.
creds() { printf 'user = "%s:%s"\n' "$SARAL_EMAIL" "$SARAL_TOKEN"; }

CODE=0
fetch() { # fetch <method> <path> <slot> [body]
  local method="$1" path="$2" slot="$3" body="${4:-}"
  if [ "$method" = GET ]; then
    CODE=$(curl -sS --config <(creds) -H 'Accept: application/json' \
      -o "$TMP/$slot" -w '%{http_code}' "https://$SARAL_SITE$path") || CODE=000
  else
    CODE=$(curl -sS --config <(creds) -X "$method" \
      -H 'Accept: application/json' -H 'Content-Type: application/json' -d "$body" \
      -o "$TMP/$slot" -w '%{http_code}' "https://$SARAL_SITE$path") || CODE=000
  fi
}

scrub() { # scrub <slot> <fixture> <what>
  local slot="$1" fixture="$2" what="$3"
  if ! SARAL_SITE="$SARAL_SITE" SARAL_EMAIL="$SARAL_EMAIL" \
    python3 "$ROOT/scripts/scrub.py" "$TMP/$slot" "$OUT/$fixture"; then
    cp "$TMP/$slot" "$OUT/${fixture%.json}.raw"
    printf '  %-34s %s  %s  !! not JSON, kept as %s\n' "$fixture" "$CODE" "$what" "${fixture%.json}.raw"
    return
  fi
  printf '  %-34s %s  %s\n' "$fixture" "$CODE" "$what"
}

capture() { # capture <method> <path> <fixture> [body]
  local method="$1" path="$2" fixture="$3" body="${4:-}"
  fetch "$method" "$path" slot "$body"
  scrub slot "$fixture" "$method $path"
}

# pick <file> <what> — pull one value out of a captured response, or print nothing
pick() {
  python3 "$ROOT/scripts/pick.py" "$1" "$2" 2>/dev/null || true
}

echo "capturing from $SARAL_SITE into $OUT"
echo

echo "capability probe (P1.3)"
capture GET "/rest/api/3/myself" myself.json
capture GET "/rest/api/3/configuration" configuration.json

# One token cannot produce both permission fixtures. Name the file for what this token actually is,
# and leave the other one as it stands.
fetch GET "/rest/api/3/mypermissions?permissions=BULK_CHANGE,MOVE_ISSUES,CREATE_ISSUES,DELETE_ISSUES,EDIT_ISSUES,DELETE_ALL_COMMENTS,ADMINISTER" perms
scrub perms mypermissions_capture.json "GET /rest/api/3/mypermissions"
if [ "$(pick "$OUT/mypermissions_capture.json" administers)" = "yes" ]; then
  PERMS_AS=mypermissions_admin.json
else
  PERMS_AS=mypermissions_basic.json
fi
mv "$OUT/mypermissions_capture.json" "$OUT/$PERMS_AS"
echo "  -> saved as $PERMS_AS (this token's own permissions)"

# The Plans API lives at /plans/plan — the doubled segment is correct, /rest/api/3/plans does not
# exist. 403 is the normal answer and is a capability result, not a failure.
fetch GET "/rest/api/3/plans/plan?maxResults=5" plans
if [ "$CODE" = "403" ]; then
  scrub plans plans_403.json "GET /rest/api/3/plans/plan"
else
  scrub plans plans_ok.json "GET /rest/api/3/plans/plan"
  echo "  -> this token reaches the Plans API; plans_403.json is unchanged and still hand-authored"
fi

echo
echo "fields and create schema (P2.2)"
capture GET "/rest/api/3/field" field.json

# The old /issue/createmeta?projectKeys=&expand= is deprecated; the replacement is a pair.
capture GET "/rest/api/3/issue/createmeta/$SARAL_PROJECT/issuetypes" createmeta_issuetypes.json
FIRST_TYPE=$(pick "$OUT/createmeta_issuetypes.json" first-issue-type)
BUG_TYPE=$(pick "$OUT/createmeta_issuetypes.json" bug-issue-type)
[ -n "$FIRST_TYPE" ] && capture GET "/rest/api/3/issue/createmeta/$SARAL_PROJECT/issuetypes/$FIRST_TYPE" createmeta_task.json
if [ -n "$BUG_TYPE" ] && [ "$BUG_TYPE" != "$FIRST_TYPE" ]; then
  capture GET "/rest/api/3/issue/createmeta/$SARAL_PROJECT/issuetypes/$BUG_TYPE" createmeta_bug.json
else
  echo "  !! no second issue type to capture; createmeta_bug.json is unchanged"
fi

echo
echo "issues, ADF and paging (P1.2, P1.4)"
capture GET "/rest/api/3/issue/$SARAL_ISSUE" issue_rich_adf.json
capture GET "/rest/api/3/issue/$SARAL_ISSUE/transitions?expand=transitions.fields" transitions.json
capture GET "/rest/api/3/issue/$SARAL_ISSUE/comment" comments.json

SEARCH_FIELDS='["summary","status","assignee","issuetype","priority","updated"]'
capture POST "/rest/api/3/search/jql" search_page1.json \
  "{\"jql\":\"project = $SARAL_PROJECT ORDER BY created DESC\",\"maxResults\":5,\"fields\":$SEARCH_FIELDS}"

# The paginator tests need a real second page, so follow the token rather than asking a human to.
NEXT=$(pick "$OUT/search_page1.json" next-page-token)
if [ -n "$NEXT" ]; then
  capture POST "/rest/api/3/search/jql" search_page2.json \
    "{\"jql\":\"project = $SARAL_PROJECT ORDER BY created DESC\",\"maxResults\":5,\"fields\":$SEARCH_FIELDS,\"nextPageToken\":\"$NEXT\"}"
else
  echo "  !! page one reported no nextPageToken — pick a project with more than 5 issues"
fi
capture POST "/rest/api/3/search/approximate-count" approximate_count.json \
  "{\"jql\":\"project = $SARAL_PROJECT\"}"

echo
echo "versions (P5.1)"
capture GET "/rest/api/3/project/$SARAL_PROJECT/version?maxResults=50" versions.json

echo
echo "boards and sprints (P6.1, P6.2)"
capture GET "/rest/agile/1.0/board?projectKeyOrId=$SARAL_PROJECT" board.json

# Both board-configuration shapes matter, and which board has which is per-site — so look.
BOARDS=$(pick "$OUT/board.json" board-ids)
GOT_EST="" GOT_NONE="" SCRUM_BOARD=""
for id in $BOARDS; do
  fetch GET "/rest/agile/1.0/board/$id/configuration" cfg
  KIND=$(pick "$TMP/cfg" estimation-type)
  if [ "$KIND" = "none" ] && [ -z "$GOT_NONE" ]; then
    scrub cfg board_config_no_estimation.json "GET /rest/agile/1.0/board/$id/configuration"
    GOT_NONE=$id
  elif [ "$KIND" != "none" ] && [ -z "$GOT_EST" ]; then
    scrub cfg board_config_estimation.json "GET /rest/agile/1.0/board/$id/configuration"
    GOT_EST=$id
  fi
  [ -z "$SCRUM_BOARD" ] && SCRUM_BOARD=$id
  [ -n "$GOT_EST" ] && [ -n "$GOT_NONE" ] && break
done
[ -z "$GOT_EST" ] && echo "  !! no board with estimation; board_config_estimation.json is unchanged"
[ -z "$GOT_NONE" ] && echo "  !! no board with estimation type none; board_config_no_estimation.json is unchanged"
if [ -n "$SCRUM_BOARD" ]; then
  capture GET "/rest/agile/1.0/board/$SCRUM_BOARD/sprint?maxResults=10" sprint_page.json
else
  echo "  !! no board on $SARAL_PROJECT; sprint_page.json is unchanged"
fi

cat <<'NOTE'

Done. These fixtures cannot be captured and stay hand-authored:

  rate_limited.json        429 with Retry-After
  validation_error.json    400 with per-field errors
  bulkmove_submit.json     and bulkmove_task_{enqueued,running,complete,failed}.json
  the mypermissions variant this token is not

Now reconcile and read the diff:

  1. git diff pkg/jira/jiratest/fixtures/ — check for real names, emails, account ids, your site
     host, and anything in a summary or comment body you would not publish. The scrubber handles
     structure, not prose.
  2. pkg/jira/jiratest/fixtures_test.go pins the fixture inventory in
     TestFixtures_CoverEveryResponseTheServerReplays. Add createmeta_issuetypes.json (and plans_ok.json
     if you captured one) to that list, or delete the files.
  3. pkg/jira/jiratest/server.go dispatches createmeta on srvBugIssueTypeID and reads the page-one
     token out of search_page1.json. Point the const at the issue type you actually captured.
  4. TestFixtures_RichDescriptionExercisesTheNodesARendererMustHandle asserts which ADF node types
     the description contains. Run the census and update it to what your issue really has:
       go test ./pkg/jira/jiratest/ -run RichDescription -v
  5. go test ./... — green before you commit.
NOTE
