package app

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// These six keep docs/PERFORMANCE.md's promise about the budgets mechanical
// rather than honour-system. They are the only budget tests built with the race
// detector, because they measure nothing: they read the tree.

const (
	perfDoc      = "docs/PERFORMANCE.md"
	ciWorkflow   = ".github/workflows/ci.yml"
	guardsOpen   = "<!-- budget-guards -->"
	guardsClose  = "<!-- /budget-guards -->"
	guardsAnswer = "add it to the table between the budget-guards markers in " + perfDoc +
		", or delete the guard and the row together — which is how this repo says out loud " +
		"that a budget is no longer held"
	gateScript = "scripts/benchgate.py"
	gateTest   = "scripts/benchgate_test.py"
)

var (
	guardDecl  = regexp.MustCompile(`(?m)^func (TestBudget_\w+)\(`)
	testDecl   = regexp.MustCompile(`(?m)^func (Test\w*)\(`)
	noDetector = regexp.MustCompile(`(?m)^//go:build [^\n]*!race`)

	// The two shapes a wall-clock budget takes here. A relational operator
	// against a fixed duration is the giveaway: the corpus compares durations
	// for correctness with == and !=, passes timeouts as arguments, and bounds
	// a polling loop with a deadline and After.
	perOp      = regexp.MustCompile(`\bNsPerOp\(\)`)
	clockBound = regexp.MustCompile(`[<>]=?[^=<>]*\btime\.(?:Nanosecond|Microsecond|Millisecond|Second|Minute|Hour)\b`)

	// The pieces of the ratio rule. A timing is read either off a variable a
	// benchmark result was put in or off the call itself; the operators are the
	// ones that can put two of them on the same side of a pass or fail.
	nsInline  = regexp.MustCompile(`testing\.Benchmark\((.*?)\)\.NsPerOp\(\)`)
	nsRead    = regexp.MustCompile(`(\w+)\.NsPerOp\(\)`)
	nsAssign  = regexp.MustCompile(`^\s*(?:\w+\s+)?(\w+(?:\s*,\s*\w+)*)\s*:?=[^=]`)
	runsBench = regexp.MustCompile(`\btesting\.Benchmark\(`)
	combine   = regexp.MustCompile(`[-+*/]|[<>]=?`)
	joinOp    = regexp.MustCompile(`[-+*/(,]$`)
	literal   = regexp.MustCompile("\"(?:[^\"\\\\]|\\\\.)*\"|`[^`]*`")
	funcTop   = regexp.MustCompile(`^func\b`)
)

type guard struct{ pkg, test string }

// The table in the document is the inventory of what holds the budgets, so it
// has to name every guard and only guards that exist. Checked here rather than
// only in the budgets job because a job can be deleted and this cannot: it fails
// in the suite everything else already runs in.
func TestBudget_TheDocumentNamesEveryGuardAndOnlyRealOnes(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	listed := guardsInTheDocument(t, root)
	found := guardsInTheTree(t, root)

	for _, g := range missing(listed, found) {
		t.Errorf("%s names %s in %s and no such test exists there", perfDoc, g.test, g.pkg)
	}
	for _, g := range missing(found, listed) {
		t.Errorf("%s defines %s and %s does not name it; %s", g.pkg, g.test, perfDoc, guardsAnswer)
	}
}

// A guard built `!race` runs in no suite CI has unless CI has a suite without
// the detector, so the lane is half the guarantee and this is what keeps it
// there.
func TestBudget_CIRunsTheGuardsWithoutTheDetector(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	content, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(ciWorkflow)))
	if err != nil {
		t.Fatalf("reading %s: %v", ciWorkflow, err)
	}

	lane, jailed := budgetLane(string(content))
	switch {
	case lane == "":
		t.Fatalf("%s no longer runs the budget guards. Every one of them is built `//go:build !race` "+
			"and the rest of CI is the race suite, so without a lane that drops -race and selects "+
			"%s they run nowhere at all", ciWorkflow, "'^TestBudget_'")
	case strings.Contains(lane, "-race"):
		t.Errorf("the budget lane in %s runs with the race detector: %s\n"+
			"The detector puts about twenty times the cost on these paths, so the numbers it "+
			"reports are the instrumentation's and not the binary's", ciWorkflow, lane)
	}
	if !jailed {
		t.Errorf("the budget lane in %s runs outside the network namespace: %s\n"+
			"docs/TESTING.md says no test opens a non-loopback connection, and a lane that runs "+
			"tests the race suite skips is a lane where that stops being checked", ciWorkflow, lane)
	}
	if !strings.Contains(string(content), perfDoc) {
		t.Errorf("%s no longer reads the guard table out of %s, so the set of guards that ran "+
			"is compared against nothing", ciWorkflow, perfDoc)
	}
}

// budgetLane returns the workflow's `go test` line that selects the budget
// guards, and whether it sits inside an `unshare -n` block.
func budgetLane(content string) (lane string, jailed bool) {
	return goTestLane(content, func(line string) bool { return strings.Contains(line, "TestBudget_") })
}

// goTestLane returns the first `go test` line the predicate accepts, and whether
// it sits inside an `unshare -n` block. The block runs from the unshare to the
// next list item indented outside it, which in a workflow is the next step.
func goTestLane(content string, want func(string) bool) (lane string, jailed bool) {
	jailIndent := -1
	for _, line := range strings.Split(content, "\n") {
		body := strings.TrimLeft(line, " ")
		indent := len(line) - len(body)
		if jailIndent >= 0 && indent < jailIndent && strings.HasPrefix(body, "- ") {
			jailIndent = -1
		}
		if strings.Contains(line, "unshare -n") {
			jailIndent = indent
		}
		if strings.Contains(line, "go test") && want(line) {
			return strings.TrimSpace(line), jailIndent >= 0
		}
	}
	return "", false
}

func guardsInTheDocument(t *testing.T, root string) []guard {
	t.Helper()

	content, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(perfDoc)))
	if err != nil {
		t.Fatalf("reading %s: %v", perfDoc, err)
	}
	text := string(content)
	open := strings.Index(text, guardsOpen)
	end := strings.Index(text, guardsClose)
	if open < 0 || end < open {
		t.Fatalf("%s no longer has a guard table between %s and %s", perfDoc, guardsOpen, guardsClose)
	}

	var out []guard
	for _, line := range strings.Split(text[open+len(guardsOpen):end], "\n") {
		cells := strings.Split(strings.TrimSpace(line), "|")
		if len(cells) != 4 {
			continue
		}
		pkg := strings.Trim(strings.TrimSpace(cells[1]), "`")
		test := strings.Trim(strings.TrimSpace(cells[2]), "`")
		if !strings.HasPrefix(test, "TestBudget_") {
			continue
		}
		out = append(out, guard{pkg: pkg, test: test})
	}
	if len(out) == 0 {
		t.Fatalf("the guard table in %s is empty, so nothing holds the budgets it lists", perfDoc)
	}
	return out
}

// skipWalk keeps the walk inside this checkout. A dot-directory is where a
// nested worktree lives, and every guard in it is a second copy of one already
// counted — internal/arch's walker skips the same shapes for the same reason.
func skipWalk(name string) bool {
	return name == "testdata" || name == "vendor" ||
		strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_")
}

func guardsInTheTree(t *testing.T, root string) []guard {
	t.Helper()

	var out []guard
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		switch {
		case err != nil:
			return err
		case d.IsDir() && skipWalk(d.Name()):
			return fs.SkipDir
		case d.IsDir() || !strings.HasSuffix(d.Name(), "_test.go"):
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, filepath.Dir(path))
		if err != nil {
			return err
		}
		for _, m := range guardDecl.FindAllStringSubmatch(string(content), -1) {
			out = append(out, guard{pkg: filepath.ToSlash(rel), test: m[1]})
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking the tree for budget guards: %v", err)
	}
	return out
}

func missing(want, have []guard) []guard {
	held := make(map[guard]bool, len(have))
	for _, g := range have {
		held[g] = true
	}
	var out []guard
	for _, g := range want {
		if !held[g] {
			out = append(out, g)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].pkg != out[j].pkg {
			return out[i].pkg < out[j].pkg
		}
		return out[i].test < out[j].test
	})
	return out
}

func repoRoot(t *testing.T) string {
	t.Helper()

	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("working directory: %v", err)
	}
	for {
		_, err := os.Stat(filepath.Join(dir, "go.mod"))
		if err == nil {
			return dir
		}
		if !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("looking for go.mod in %s: %v", dir, err)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("no go.mod in any parent of the working directory")
		}
		dir = parent
	}
}

// A wall-clock assertion outside a budget file is the hole the other two guards
// leave open: the race suite builds it, the detector inflates what it measures
// about twentyfold, and because the name is not TestBudget_ the table above
// never misses it. Three palette assertions, two form ones and cmd/saral's
// first paint sat in that hole and were the whole of a suite that failed about
// half the time, each run on a different one of them.
func TestBudget_EveryWallClockAssertionSitsInAGuard(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		switch {
		case err != nil:
			return err
		case d.IsDir() && skipWalk(d.Name()):
			return fs.SkipDir
		case d.IsDir() || !strings.HasSuffix(d.Name(), "_test.go"):
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		holdOneFile(t, filepath.ToSlash(rel), string(content))
		return nil
	})
	if err != nil {
		t.Fatalf("walking the tree for wall-clock assertions: %v", err)
	}
}

// holdOneFile checks the two halves of the mechanism on one test file: a
// wall-clock assertion belongs in a budget file, and a test in a budget file has
// to carry the name the only lane that runs one selects on.
func holdOneFile(t *testing.T, rel, content string) {
	t.Helper()

	named := strings.HasSuffix(rel, "budget_test.go")
	tagged := noDetector.MatchString(content)

	if named && tagged {
		for _, m := range testDecl.FindAllStringSubmatch(content, -1) {
			if !strings.HasPrefix(m[1], "TestBudget_") {
				t.Errorf("%s is built without the detector and defines %s. The budget lane selects "+
					"%s and the race suite does not build this file, so it runs in neither: rename it",
					rel, m[1], "'^TestBudget_'")
			}
		}
		return
	}

	for i, line := range strings.Split(content, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "//") {
			continue
		}
		var what string
		switch {
		case perOp.MatchString(line):
			what = "reads a benchmark's ns/op"
		case clockBound.MatchString(line):
			what = "compares a duration against a fixed bound"
		default:
			continue
		}
		fix := "move it to a budget_test.go beside it, built without the detector, as a TestBudget_"
		if named {
			fix = "add the !race build constraint to this file"
		}
		t.Errorf("%s:%d %s, which makes it a wall-clock budget, and this file is built with the race "+
			"detector. The detector puts about twenty times the cost on the paths these measure, so "+
			"what the assertion reads is the instrumentation and it fails on whichever run loses the "+
			"lottery: %s, and %s\n\t%s", rel, i+1, what, fix, guardsAnswer, strings.TrimSpace(line))
	}
}

// A budget that divides one benchmark's ns/op by another's is unsound wherever
// it lives, so this is not the wall-clock rule above with a different tag on it.
// `testing.Benchmark` picks its own iteration count per call and each call meets
// its own neighbours, so the two samples are not comparable: the date cascade's
// linearity guard read 11.4ms for two thousand issues against 2.4ms for another
// run of the same two thousand — slower than the fastest ten-thousand run — while
// the allocation counts repeated to the unit. Allocation and byte counts between
// two benchmarks are deterministic and are what this repo compares deliberately;
// it is the timings that may not be put on both sides of an operator.
func TestBudget_NoBudgetDividesOneBenchmarksTimeByAnothers(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		switch {
		case err != nil:
			return err
		case d.IsDir() && skipWalk(d.Name()):
			return fs.SkipDir
		case d.IsDir() || !strings.HasSuffix(d.Name(), "_test.go"):
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		holdOneFileAgainstRatios(t, filepath.ToSlash(rel), string(content))
		return nil
	})
	if err != nil {
		t.Fatalf("walking the tree for ratios between benchmark timings: %v", err)
	}
}

// holdOneFileAgainstRatios reports the first statement in each function that
// puts two different benchmarks' timings into one arithmetic or relational
// expression. One report a function, because the shape arrives as a division and
// an assertion on what it produced, and both lines are the same mistake.
func holdOneFileAgainstRatios(t *testing.T, rel, content string) {
	t.Helper()

	timed := map[string]map[string]bool{}
	said := false
	for _, stmt := range statements(content) {
		if funcTop.MatchString(stmt.text) {
			timed, said = map[string]map[string]bool{}, false
		}
		code := literal.ReplaceAllString(stmt.text, `""`)
		if cut := strings.Index(code, "//"); cut >= 0 {
			code = code[:cut]
		}
		for _, part := range splitTerms(code) {
			from := timingsIn(part, timed)
			if len(from) < 2 || !combine.MatchString(part) {
				continue
			}
			if !said {
				said = true
				t.Errorf("%s:%d derives a budget from the timings of %s in one expression. Each "+
					"testing.Benchmark call picks its own iteration count and meets its own "+
					"neighbours, so a ratio or a difference between two of their ns/op figures is a "+
					"ratio between two independent samples of what the machine was doing: one 2k run "+
					"here read 11.4ms against another 2k run's 2.4ms while the allocation counts "+
					"repeated to the unit. Compare a timing against a fixed bound, and compare the "+
					"two runs on allocations or bytes, which are deterministic\n\t%s",
					rel, stmt.line, strings.Join(sorted(from), " and "), strings.TrimSpace(stmt.text))
			}
		}
		if names := nsAssign.FindStringSubmatch(code); names != nil {
			from := timingsIn(strings.TrimPrefix(code, names[0]), timed)
			for _, name := range strings.Split(names[1], ",") {
				name = strings.TrimSpace(name)
				if name == "" || name == "_" || len(from) == 0 {
					continue
				}
				if timed[name] == nil {
					timed[name] = map[string]bool{}
				}
				for src := range from {
					timed[name][src] = true
				}
			}
		}
	}
}

// timingsIn names the benchmarks whose ns/op the expression reads, directly or
// through a variable one was put in.
func timingsIn(code string, timed map[string]map[string]bool) map[string]bool {
	from := map[string]bool{}
	rest := nsInline.ReplaceAllStringFunc(code, func(m string) string {
		from[strings.TrimSpace(nsInline.FindStringSubmatch(m)[1])] = true
		return ""
	})
	for _, m := range nsRead.FindAllStringSubmatch(rest, -1) {
		from[m[1]] = true
	}
	for name, srcs := range timed {
		if !wordIn(name, rest) {
			continue
		}
		for src := range srcs {
			from[src] = true
		}
	}
	return from
}

// splitTerms cuts a statement where no operator reaches across, so that two
// benchmarks each held against a bound of its own — one ns/op over zero and then
// the other, joined by an and — is not read as one held against the other.
func splitTerms(code string) []string {
	for _, sep := range []string{"&&", "||", ";"} {
		code = strings.ReplaceAll(code, sep, ",")
	}
	return strings.Split(code, ",")
}

type statement struct {
	line int
	text string
}

// statements joins the lines gofmt wrapped back into one, so an expression too
// long for a line is read whole: a division split after its operator, and a call
// whose arguments run on. Full-line comments are dropped rather than joined, so
// prose in them cannot be read as code.
func statements(content string) []statement {
	var out []statement
	var held strings.Builder
	start, depth := 0, 0
	for i, line := range strings.Split(content, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "//") {
			continue
		}
		if held.Len() == 0 {
			start = i + 1
		} else {
			held.WriteString(" ")
		}
		held.WriteString(strings.TrimSpace(line))
		bare := literal.ReplaceAllString(line, `""`)
		depth += strings.Count(bare, "(") + strings.Count(bare, "[") -
			strings.Count(bare, ")") - strings.Count(bare, "]")
		if depth > 0 || joinOp.MatchString(strings.TrimSpace(bare)) {
			continue
		}
		out = append(out, statement{line: start, text: held.String()})
		held.Reset()
		depth = 0
	}
	if held.Len() > 0 {
		out = append(out, statement{line: start, text: held.String()})
	}
	return out
}

// wordIn reports whether the expression names the identifier, rather than
// merely containing its letters inside a longer one.
func wordIn(name, code string) bool {
	part := func(r byte) bool {
		return r == '_' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9'
	}
	for at := 0; at < len(code); {
		i := strings.Index(code[at:], name)
		if i < 0 {
			return false
		}
		i += at
		before := i == 0 || !part(code[i-1])
		end := i + len(name)
		if before && (end == len(code) || !part(code[end])) {
			return true
		}
		at = i + 1
	}
	return false
}

func sorted(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Running a benchmark from a test is the shape both other guards were written
// about, and neither of them covers the allocation half. The detector puts about
// twenty times the cost on these paths, and an allocation count comes from
// process-wide MemStats, so a benchmark run beside another test is handed that
// test's allocations divided by its own iteration count — 733 for a scroll that
// costs 1. A parallel test in a race-built file gets both at once, which is
// where the form's and the palette's scroll assertions sat: outside the table,
// under the detector, and beside whatever else the package was running.
func TestBudget_NoTestOutsideAGuardRunsABenchmark(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		switch {
		case err != nil:
			return err
		case d.IsDir() && skipWalk(d.Name()):
			return fs.SkipDir
		case d.IsDir() || !strings.HasSuffix(d.Name(), "_test.go"):
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		holdOneFileAgainstLooseBenchmarks(t, filepath.ToSlash(rel), string(content))
		return nil
	})
	if err != nil {
		t.Fatalf("walking the tree for benchmarks run from tests: %v", err)
	}
}

// holdOneFileAgainstLooseBenchmarks reports a file that calls testing.Benchmark
// and is not a guard file: either the detector builds it, or the name the only
// lane that runs one selects on is missing.
func holdOneFileAgainstLooseBenchmarks(t *testing.T, rel, content string) {
	t.Helper()

	if !runsBench.MatchString(content) {
		return
	}
	if !noDetector.MatchString(content) {
		t.Errorf("%s runs a benchmark from a test and is built with the race detector. What the "+
			"benchmark reports there is the instrumentation's cost and its neighbours' allocations: "+
			"move it to a budget_test.go beside it, built without the detector, as a TestBudget_, "+
			"and %s\n\t%s", rel, guardsAnswer, firstMatch(runsBench, content))
		return
	}
	for _, m := range testDecl.FindAllStringSubmatch(content, -1) {
		if !strings.HasPrefix(m[1], "TestBudget_") {
			t.Errorf("%s runs a benchmark from %s, and the lane that runs a file built without the "+
				"detector selects %s. Rename it, or it runs in no suite at all",
				rel, m[1], "'^TestBudget_'")
		}
	}
}

// firstMatch is the line the report quotes, so the message names a place and not
// only a file.
func firstMatch(re *regexp.Regexp, content string) string {
	for _, line := range strings.Split(content, "\n") {
		if re.MatchString(line) {
			return strings.TrimSpace(line)
		}
	}
	return ""
}

// A ceiling catches a step past a number and cannot catch a slide that stays
// under one, which is what the regression lane is for: it benchmarks this commit
// and the base commit on one runner and fails on a budgeted path that allocates
// more than it did. The lane can be deleted and nothing else would notice, so
// this is what notices — in the suite everything already runs in.
func TestBudget_CIComparesTheBenchmarksAgainstTheBaseBranch(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	content, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(ciWorkflow)))
	if err != nil {
		t.Fatalf("reading %s: %v", ciWorkflow, err)
	}
	text := string(content)

	lane, jailed := goTestLane(text, func(line string) bool { return strings.Contains(line, "-bench") })
	switch {
	case lane == "":
		t.Fatalf("%s no longer benchmarks anything. A budget ceiling fails on a step past a number "+
			"and passes a path that got thirty per cent slower under it, which is what comparing a "+
			"run against the base commit is for", ciWorkflow)
	case strings.Contains(lane, "-race"):
		t.Errorf("the benchmark lane in %s runs with the race detector: %s\n"+
			"The detector puts about twenty times the cost on these paths, so what it compares is "+
			"the instrumentation on both sides", ciWorkflow, lane)
	}
	if !jailed {
		t.Errorf("the benchmark lane in %s runs outside the network namespace: %s\n"+
			"docs/TESTING.md says no test opens a non-loopback connection, and a lane that runs "+
			"benchmarks the race suite skips is a lane where that stops being checked", ciWorkflow, lane)
	}

	for _, want := range []struct{ needle, why string }{
		{"benchstat", "nothing turns the two runs into a comparison"},
		{gateScript, "nothing applies the rule to the comparison, so the numbers are printed and read by nobody"},
		{gateTest, "the gate has never been seen to fail, which makes it a claim rather than a check"},
		{"pull_request.base.sha", "there is no second tree to compare against, so a baseline would have to be stored — and a stored one goes stale the first time a real improvement lands"},
	} {
		if !strings.Contains(text, want.needle) {
			t.Errorf("%s no longer names %s, so %s", ciWorkflow, want.needle, want.why)
		}
	}
}
