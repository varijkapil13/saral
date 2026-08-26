#!/usr/bin/env python3
"""Tests for scripts/checkleak.py.

    python3 scripts/checkleak_test.py

A leak check nobody has run against a planted leak is a claim, not a check, so
every assertion here is either "this tree is clean" or "this tree is not, and the
message says which file". Standard library only: CI runs this before it runs the
check itself, on a runner with nothing installed but Go.
"""

import contextlib
import io
import json
import os
import shutil
import subprocess
import sys
import tempfile
import unittest

SCRIPTS = os.path.dirname(os.path.abspath(__file__))
ROOT = os.path.dirname(SCRIPTS)
sys.path.insert(0, SCRIPTS)
# Importing the check is the only reason anything here is a module, and a
# __pycache__ beside it is a directory nobody has a reason to gitignore.
sys.dont_write_bytecode = True

import checkleak  # noqa: E402


def write(root, files):
    for name, body in files.items():
        path = os.path.join(root, *name.split("/"))
        os.makedirs(os.path.dirname(path), exist_ok=True)
        with open(path, "w", encoding="utf-8") as f:
            f.write(body if isinstance(body, str) else json.dumps(body, indent=2))
    return root


def run(*argv):
    out = io.StringIO()
    with contextlib.redirect_stdout(out):
        code = checkleak.main(list(argv))
    return code, out.getvalue()


def git(root, *args):
    env = dict(os.environ, GIT_CONFIG_GLOBAL=os.devnull, GIT_CONFIG_SYSTEM=os.devnull)
    return subprocess.run(["git", "-C", root, *args], capture_output=True, text=True,
                          check=True, env=env)


class Trees(unittest.TestCase):
    def setUp(self):
        self.dir = tempfile.mkdtemp(prefix="checkleak-")
        self.addCleanup(shutil.rmtree, self.dir, True)
        self.live = os.path.join(self.dir, "live")
        self.fix = os.path.join(self.dir, "fixtures")
        os.makedirs(self.live)
        os.makedirs(self.fix)


class TestTheCheckCatchesALeak(Trees):
    def test_a_summary_copied_out_of_a_capture(self):
        write(self.live, {"search.json": {"summary": "Rollout of the Fenwick ledger import"}})
        write(self.fix, {"search_page1.json": {"summary": "Rollout of the Fenwick ledger import"}})

        code, out = run(self.live, self.fix)

        self.assertEqual(code, 1, out)
        self.assertIn("search_page1.json", out)
        self.assertIn("phrase:rollout of the fenwick ledger import", out)

    def test_one_distinctive_word_reused_in_a_sentence_of_its_own(self):
        write(self.live, {"board.json": {"name": "Ledgerbridge delivery board"}})
        write(self.fix, {"board.json": {"name": "The Ledgerbridge cutover"}})

        code, out = run(self.live, self.fix)

        self.assertEqual(code, 1, out)
        self.assertIn("ledgerbridge", out)

    def test_a_fixture_in_a_subdirectory_is_read_like_any_other(self):
        write(self.live, {"board.json": {"name": "Fenwick delivery board"}})
        write(self.fix, {"agile/board/board.json": {"name": "Fenwick delivery board"}})

        code, out = run(self.live, self.fix)

        self.assertEqual(code, 1, out)
        self.assertIn(os.path.join("agile", "board", "board.json"), out)


class TestTheCheckPassesACleanTree(Trees):
    def test_invented_words_beside_a_capture_that_shares_none_of_them(self):
        write(self.live, {"search.json": {"summary": "Rollout of the Fenwick ledger import"}})
        write(self.fix, {"search_page1.json": {"summary": "Checkout fails when the basket is empty"}})

        code, out = run(self.live, self.fix)

        self.assertEqual(code, 0, out)
        self.assertIn("clean", out)

    def test_an_http_reason_phrase_shared_by_both_is_not_a_leak(self):
        problem = {"type": "about:blank", "title": "Not Found", "status": 404}
        write(self.live, {"problem.json": problem})
        write(self.fix, {"problem_no_endpoint.json": problem})

        code, out = run(self.live, self.fix)

        self.assertEqual(code, 0, out)

    def test_every_reason_phrase_a_jira_site_answers_with(self):
        for phrase in ["Bad Request", "Unauthorized", "Forbidden", "Not Found",
                       "Method Not Allowed", "Conflict", "Too Many Requests",
                       "Internal Server Error", "Service Unavailable"]:
            with self.subTest(phrase=phrase):
                self.assertEqual(checkleak.distinctive(phrase), set())

    def test_a_reason_phrase_does_not_excuse_the_same_word_inside_a_sentence(self):
        self.assertIn("phrase:forbidden while the fenwick cutover runs",
                      checkleak.distinctive("Forbidden while the Fenwick cutover runs"))
        self.assertIn("conflict", checkleak.distinctive("Conflict with the Fenwick rollout"))


class TestTheTreeHasToHoldOnItsOwn(Trees):
    def test_a_host_that_is_not_the_invented_site(self):
        write(self.fix, {"myself.json": {"self": "https://acme.atlassian.net/rest/api/3/myself"}})

        code, out = run(self.live, self.fix)

        self.assertEqual(code, 1, out)
        self.assertIn("acme.atlassian.net", out)

    def test_an_avatar_host_the_atlassian_net_rule_does_not_cover(self):
        write(self.fix, {"myself.json": {"avatar": "https://api.media.atlassian.com/file/abc"}})

        code, out = run(self.live, self.fix)

        self.assertEqual(code, 1, out)
        self.assertIn("api.media.atlassian.com", out)

    def test_a_link_to_a_company_intranet_inside_a_description(self):
        write(self.fix, {"issue.json": {"description": "see https://wiki.acme.example.org/runbook"}})

        code, out = run(self.live, self.fix)

        self.assertEqual(code, 1, out)
        self.assertIn("wiki.acme.example.org", out)

    def test_a_fixture_that_does_not_parse_is_not_quietly_skipped(self):
        write(self.fix, {"broken.json": "{ this is not json"})

        code, out = run(self.live, self.fix)

        self.assertEqual(code, 1, out)
        self.assertIn("broken.json", out)
        self.assertIn("cannot read", out)

    def test_an_empty_tree_is_not_a_pass(self):
        code, out = run(self.live, self.fix)

        self.assertEqual(code, 1, out)
        self.assertIn("reading nothing", out)

    def test_a_missing_tree_is_not_a_pass(self):
        code, out = run(self.live, os.path.join(self.dir, "gone"))

        self.assertEqual(code, 1, out)
        self.assertIn("reading nothing", out)

    def test_the_site_the_fixtures_are_supposed_to_name(self):
        write(self.fix, {"board.json": {"self": "https://example.atlassian.net/rest/agile/1.0/board/10"}})

        code, out = run(self.live, self.fix)

        self.assertEqual(code, 0, out)


class TestTheDifferentialHalfIsNeverAssumed(Trees):
    def test_no_capture_says_so_rather_than_reporting_clean(self):
        write(self.fix, {"board.json": {"name": "EX Scrum board"}})

        code, out = run(os.path.join(self.dir, "gone"), self.fix)

        self.assertEqual(code, 0, out)
        self.assertIn("nothing was compared", out)

    def test_require_capture_fails_when_there_is_nothing_to_compare(self):
        write(self.fix, {"board.json": {"name": "EX Scrum board"}})

        code, out = run(os.path.join(self.dir, "gone"), self.fix, "--require-capture")

        self.assertEqual(code, 1, out)
        self.assertIn("--require-capture", out)

    def test_require_capture_passes_once_the_capture_is_there(self):
        write(self.live, {"board.json": {"name": "Fenwick delivery board"}})
        write(self.fix, {"board.json": {"name": "EX Scrum board"}})

        code, out = run(self.live, self.fix, "--require-capture")

        self.assertEqual(code, 0, out)


@unittest.skipIf(shutil.which("git") is None, "git is not installed")
class TestTheCaptureStaysOutOfTheTrackedTree(Trees):
    def repo(self, ignore=f"/{checkleak.CAPTURE_DIR}/\n"):
        root = os.path.join(self.dir, "repo")
        os.makedirs(root)
        git(root, "init", "-q")
        write(root, {".gitignore": ignore})
        return root

    def test_a_checkout_that_still_ignores_it(self):
        problems, note = checkleak.untracked_capture(self.repo())

        self.assertEqual(problems, [])
        self.assertIn("is ignored", note)

    def test_the_ignore_rule_deleted(self):
        problems, _ = checkleak.untracked_capture(self.repo(ignore="/dist/\n"))

        self.assertEqual(len(problems), 1, problems)
        self.assertIn("no longer ignored", problems[0])

    def test_a_capture_forced_into_the_index(self):
        root = self.repo()
        write(root, {f"{checkleak.CAPTURE_DIR}/fixtures/search.json": {"summary": "x"}})
        git(root, "add", "-f", "--", f"{checkleak.CAPTURE_DIR}/fixtures/search.json")

        problems, _ = checkleak.untracked_capture(root)

        self.assertEqual(len(problems), 1, problems)
        self.assertIn("tracked by git", problems[0])

    def test_somewhere_that_is_not_a_checkout_says_so(self):
        problems, note = checkleak.untracked_capture(self.dir)

        self.assertEqual(problems, [])
        self.assertIn("skipped", note)


class TestThisRepository(unittest.TestCase):
    def test_the_committed_fixtures_are_clean(self):
        code, out = run()

        self.assertEqual(code, 0, out)

    def test_the_script_runs_as_a_command(self):
        done = subprocess.run([sys.executable, os.path.join(SCRIPTS, "checkleak.py")],
                              capture_output=True, text=True, check=False)

        self.assertEqual(done.returncode, 0, done.stdout + done.stderr)

    def test_ci_runs_the_check_and_these_tests(self):
        with open(os.path.join(ROOT, ".github", "workflows", "ci.yml"), encoding="utf-8") as f:
            workflow = f.read()
        commands = [line.strip() for line in workflow.split("\n")
                    if line.strip().startswith(("run:", "- run:"))]

        for script in ["scripts/checkleak.py", "scripts/checkleak_test.py"]:
            with self.subTest(script=script):
                self.assertTrue(any(script in command for command in commands),
                                f"no CI step runs {script}, so the rule that no capture reaches "
                                "the fixtures is back to being asked for politely")


if __name__ == "__main__":
    unittest.main(verbosity=2)
