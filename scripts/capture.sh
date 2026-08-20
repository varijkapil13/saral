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

# Answers other than the token are remembered here, so a re-run is five Enters.
STATE_DIR="${SARAL_CONFIG_DIR:-${XDG_CONFIG_HOME:-$HOME/.config}/saral}"
STATE="$STATE_DIR/capture.env"
# shellcheck source=/dev/null
[ -r "$STATE" ] && . "$STATE"

trim() { local v="$1"; v="${v#"${v%%[![:space:]]*}"}"; printf '%s' "${v%"${v##*[![:space:]]}"}"; }

ask() { # ask <var> <prompt> [secret]
  local var="$1" prompt="$2" secret="${3:-}" value="${!1:-}" reply
  if [ -n "$value" ]; then
    [ -z "$secret" ] && echo "  $prompt: $value"
    return
  fi
  if [ ! -t 0 ]; then
    echo "capture: $var is not set and there is no terminal to ask on; export it and re-run" >&2
    exit 1
  fi
  if [ -n "$secret" ]; then
    read -rsp "$prompt: " reply; echo
  else
    read -rp "$prompt: " reply
  fi
  value="$(trim "$reply")"
  [ -n "$value" ] || { echo "capture: $var cannot be empty" >&2; exit 1; }
  printf -v "$var" '%s' "$value"
  export "${var?}"
}

# The token is the one answer that never goes in a file. The OS keychain is where
# it belongs, and it is where Saral itself reads a token from.
keychain_get() {
  case "$(uname -s)" in
    Darwin) security find-generic-password -s saral-capture -a "$1" -w 2>/dev/null;;
    *) command -v secret-tool >/dev/null && secret-tool lookup service saral-capture account "$1" 2>/dev/null;;
  esac
}

keychain_set() {
  case "$(uname -s)" in
    Darwin) security add-generic-password -U -s saral-capture -a "$1" -w "$2" 2>/dev/null;;
    *) command -v secret-tool >/dev/null &&
       printf '%s' "$2" | secret-tool store --label="saral capture token" service saral-capture account "$1" 2>/dev/null;;
  esac
}

ask SARAL_SITE    "Jira site (host only, e.g. example.atlassian.net)"
SARAL_SITE="${SARAL_SITE#https://}"
SARAL_SITE="${SARAL_SITE%/}"
ask SARAL_EMAIL   "Atlassian account email"

if [ -z "${SARAL_TOKEN:-}" ]; then
  SARAL_TOKEN="$(keychain_get "$SARAL_EMAIL" || true)"
  [ -n "$SARAL_TOKEN" ] && echo "  API token: from your keychain"
fi
REMEMBER_TOKEN=""
if [ -z "$SARAL_TOKEN" ]; then
  ask SARAL_TOKEN "API token (not echoed)" secret
  if [ -t 0 ]; then
    read -rp "Remember this token in your keychain? [y/N]: " REMEMBER_TOKEN
  fi
fi
export SARAL_TOKEN

ask SARAL_PROJECT "Project key to capture (e.g. PROJ)"
ask SARAL_ISSUE   "Issue key with a rich description (e.g. PROJ-1)"

remember() {
  mkdir -p "$STATE_DIR"
  umask 077
  {
    echo "# Written by scripts/capture.sh. The API token is deliberately not here."
    echo "SARAL_SITE=\"$SARAL_SITE\""
    echo "SARAL_EMAIL=\"$SARAL_EMAIL\""
    echo "SARAL_PROJECT=\"$SARAL_PROJECT\""
    echo "SARAL_ISSUE=\"$SARAL_ISSUE\""
  } > "$STATE"
}

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

# scrub <slot> <fixture> <what> [wanted-status]
#
# A fixture is only replaced when the call actually answered what was asked. The
# first version of this script wrote whatever came back, so one wrong project key
# quietly replaced four good fixtures with "Issue does not exist" — the failure
# looked like a successful capture right up until the tests ran.
# OK reports whether the last scrub wrote anything. scrub itself always succeeds:
# under `set -e` a non-zero return from a top-level call ends the whole run, and
# one endpoint this token cannot reach is not a reason to abandon the other
# nineteen. Failures are counted and listed at the end instead.
OK=0
scrub() {
  local slot="$1" fixture="$2" what="$3" want="${4:-2xx}"
  OK=0
  if ! wanted "$CODE" "$want"; then
    printf '  %-34s %s  %s  !! left alone: %s\n' "$fixture" "$CODE" "$what" "$(reason "$TMP/$slot")"
    FAILED=$((FAILED + 1))
    MISSED="$MISSED  $fixture ($CODE $what)"$'\n'
    return 0
  fi
  if ! SARAL_SITE="$SARAL_SITE" SARAL_EMAIL="$SARAL_EMAIL" \
    python3 "$ROOT/scripts/scrub.py" "$TMP/$slot" "$OUT/$fixture"; then
    cp "$TMP/$slot" "$OUT/${fixture%.json}.raw"
    printf '  %-34s %s  %s  !! not JSON, kept as %s\n' "$fixture" "$CODE" "$what" "${fixture%.json}.raw"
    FAILED=$((FAILED + 1))
    MISSED="$MISSED  $fixture (not JSON)"$'\n'
    return 0
  fi
  printf '  %-34s %s  %s\n' "$fixture" "$CODE" "$what"
  OK=1
}

# reason pulls Jira's own explanation out of an error body, which is the thing
# worth reading and is otherwise thrown away with the response.
reason() {
  python3 "$ROOT/scripts/pick.py" "$1" error-message 2>/dev/null || true
}

wanted() { # wanted <code> <2xx|exact code>
  case "$2" in
    2xx) case "$1" in 2??) return 0;; *) return 1;; esac;;
    *) [ "$1" = "$2" ];;
  esac
}

capture() { # capture <method> <path> <fixture> [body] [wanted-status]
  local method="$1" path="$2" fixture="$3" body="${4:-}" want="${5:-2xx}"
  fetch "$method" "$path" slot "$body"
  scrub slot "$fixture" "$method $path" "$want"
}

# pick <file> <what> — pull one value out of a captured response, or print nothing
pick() {
  python3 "$ROOT/scripts/pick.py" "$1" "$2" 2>/dev/null || true
}

FAILED=0
MISSED=""

# Jira answers an unauthenticated caller differently per endpoint rather than
# refusing outright: /mypermissions comes back 200 with every permission false,
# a search returns zero issues, and an issue is "does not exist or you do not
# have permission to see it". A whole capture can therefore look like it worked
# and be nothing but anonymous responses, so check once, up front, and stop.
echo "checking the credentials"
fetch GET "/rest/api/3/myself" whoami
if [ "$CODE" != "200" ]; then
  echo >&2
  echo "capture: $SARAL_SITE rejected these credentials (HTTP $CODE)." >&2
  echo "         $(head -c 200 "$TMP/whoami" 2>/dev/null)" >&2
  echo >&2
  echo "  Nothing was written. Check, in this order:" >&2
  echo "    * the email is the Atlassian account the token belongs to, not an alias" >&2
  echo "    * the token is current — they can be revoked at" >&2
  echo "      https://id.atlassian.com/manage-profile/security/api-tokens" >&2
  echo "    * the token is a plain API token, not one of the newer scoped ones" >&2
  echo >&2
  echo "  Verify it by hand with:" >&2
  echo "    curl -su \"\$SARAL_EMAIL:\$SARAL_TOKEN\" https://$SARAL_SITE/rest/api/3/myself" >&2
  exit 1
fi
echo "  authenticated as $(pick "$TMP/whoami" account-name)"
echo

echo "capturing from $SARAL_SITE into $OUT"
echo

echo "capability probe (P1.3)"
scrub whoami myself.json "GET /rest/api/3/myself"
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
  scrub plans plans_403.json "GET /rest/api/3/plans/plan" 403
else
  scrub plans plans_ok.json "GET /rest/api/3/plans/plan"
  echo "  -> this token reaches the Plans API; plans_403.json is unchanged and still hand-authored"
fi

echo
# A project key the token cannot see turns the rest of the run into 404 bodies.
fetch GET "/rest/api/3/project/$SARAL_PROJECT" projcheck
if [ "$CODE" != "200" ]; then
  echo >&2
  echo "capture: cannot read project $SARAL_PROJECT (HTTP $CODE) — check the key and that this" >&2
  echo "         account can browse it. Projects this token can see:" >&2
  fetch GET "/rest/api/3/project/search?maxResults=50" projlist
  pick "$TMP/projlist" project-keys >&2
  exit 1
fi

fetch GET "/rest/api/3/issue/$SARAL_ISSUE?fields=summary" issuecheck
if [ "$CODE" != "200" ]; then
  echo >&2
  echo "capture: cannot read issue $SARAL_ISSUE (HTTP $CODE) — it has to be an issue this account" >&2
  echo "         can see, and one with a rich description is the point of the exercise." >&2
  exit 1
fi

echo "fields and create schema (P2.2)"
capture GET "/rest/api/3/field" field.json

# The old /issue/createmeta?projectKeys=&expand= is deprecated; the replacement is a pair.
# Create-meta needs Create Issues in the project, which browsing it does not
# imply — so this is the call most likely to 403 for an otherwise fine token.
capture GET "/rest/api/3/issue/createmeta/$SARAL_PROJECT/issuetypes" createmeta_issuetypes.json
if [ "$OK" = 1 ]; then
  # Deliberately not the first issue type: an epic's create screen is unusual and
  # is often the one type a project will not let you create.
  FIRST_TYPE=$(pick "$OUT/createmeta_issuetypes.json" first-issue-type)
  BUG_TYPE=$(pick "$OUT/createmeta_issuetypes.json" bug-issue-type)
  [ -n "$FIRST_TYPE" ] && capture GET "/rest/api/3/issue/createmeta/$SARAL_PROJECT/issuetypes/$FIRST_TYPE" createmeta_task.json
  if [ -n "$BUG_TYPE" ] && [ "$BUG_TYPE" != "$FIRST_TYPE" ]; then
    capture GET "/rest/api/3/issue/createmeta/$SARAL_PROJECT/issuetypes/$BUG_TYPE" createmeta_bug.json
  else
    echo "  !! no second issue type to capture; createmeta_bug.json is unchanged"
  fi
else
  echo "  -> needs Create Issues in $SARAL_PROJECT; the createmeta fixtures are unchanged"
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

remember
echo
echo "Your answers are in $STATE, so the next run is five Enters. The token is not in it."
case "$REMEMBER_TOKEN" in
  [yY]*) keychain_set "$SARAL_EMAIL" "$SARAL_TOKEN" && echo "The token is in your keychain under saral-capture." ;;
esac
echo
if [ "$FAILED" -gt 0 ]; then
  echo "$FAILED call(s) did not answer as expected. Those fixtures were left exactly as they were:"
  printf '%s' "$MISSED"
  echo
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
