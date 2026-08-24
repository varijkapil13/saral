package arch

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const workflowPath = ".github/workflows/ci.yml"

const theRule = "docs/TESTING.md says no test opens a non-loopback connection, and CI is what makes that true " +
	"rather than a convention: the suite runs under `unshare -n` with only loopback up"

type looseGoTest struct {
	line int
	text string
}

type workflowScan struct {
	jail    []string
	warmUps []string
	loose   []looseGoTest
}

// scanWorkflow sorts every `go test` by where it runs. The namespace block runs
// from the `unshare -n` line to the next list item indented outside it, which in
// a workflow is the next step.
func scanWorkflow(content string) workflowScan {
	var scan workflowScan
	jailIndent := -1

	for i, line := range strings.Split(content, "\n") {
		body := strings.TrimLeft(line, " ")
		indent := len(line) - len(body)
		if jailIndent >= 0 && indent < jailIndent && strings.HasPrefix(body, "- ") {
			jailIndent = -1
		}
		if strings.Contains(line, "unshare -n") {
			jailIndent = indent
		}
		if jailIndent >= 0 {
			scan.jail = append(scan.jail, line)
		}
		if !strings.Contains(line, "go test") {
			continue
		}
		switch {
		case jailIndent >= 0:
		case strings.Contains(line, "-run") && strings.Contains(line, "^$"):
			scan.warmUps = append(scan.warmUps, strings.TrimSpace(line))
		default:
			scan.loose = append(scan.loose, looseGoTest{line: i + 1, text: strings.TrimSpace(line)})
		}
	}
	return scan
}

func TestCIWorkflow_RunsTheSuiteWithOnlyLoopbackReachable(t *testing.T) {
	t.Parallel()

	path := filepath.Join(moduleRoot(t), filepath.FromSlash(workflowPath))
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", workflowPath, err)
	}

	scan := scanWorkflow(string(content))
	if len(scan.jail) == 0 {
		t.Fatalf("%s no longer runs anything under a network namespace.\n%s", workflowPath, theRule)
	}

	jail := strings.Join(scan.jail, "\n")
	if !strings.Contains(jail, "go test") {
		t.Errorf("%s builds a network namespace and does not run the suite inside it.\n%s", workflowPath, theRule)
	}
	if !strings.Contains(jail, "curl") || !strings.Contains(jail, "exit 1") {
		t.Errorf("the namespace block in %s no longer proves that it isolates.\n"+
			"A request has to be attempted and has to fail, or an `unshare` that stopped working "+
			"reads as a hermetic suite and the green tick means nothing.", workflowPath)
	}
	for _, loose := range scan.loose {
		t.Errorf("%s:%d runs the suite with the network reachable: %s\n%s",
			workflowPath, loose.line, loose.text, theRule)
	}
}

func TestScanWorkflow_SortsEveryGoTestByWhereItRuns(t *testing.T) {
	t.Parallel()

	const jailed = `
      - name: warm the caches
        run: go test -race -count=1 -run '^$' ./...
      - name: test on a loopback-only network
        run: |
          sudo env "PATH=$PATH" \
            unshare -n -- sh -euc '
              ip link set lo up
              if curl -sS --max-time 5 -o /dev/null https://proxy.golang.org; then
                exit 1
              fi
              go test -race -count=1 ./...
            '
      - name: build
        run: go build ./...
`

	tests := []struct {
		name    string
		content string
		jail    bool
		warmUps int
		loose   []string
	}{
		{
			name:    "the workflow as this packet leaves it",
			content: jailed,
			jail:    true,
			warmUps: 1,
		},
		{
			name:    "the suite run straight, the way it was before",
			content: "      - name: test\n        run: go test -race -count=1 ./...\n",
			loose:   []string{"run: go test -race -count=1 ./..."},
		},
		{
			name:    "a second, unjailed run smuggled in after the jail",
			content: jailed + "      - name: also test\n        run: go test ./...\n",
			jail:    true,
			warmUps: 1,
			loose:   []string{"run: go test ./..."},
		},
		{
			name:    "a step with no name straight after the jail",
			content: jailed + "      - run: go test ./...\n",
			jail:    true,
			warmUps: 1,
			loose:   []string{"- run: go test ./..."},
		},
		{
			name:    "the whole jail written on one line",
			content: "      - run: sudo unshare -n -- sh -c 'ip link set lo up && curl -f https://x || exit 1; go test ./...'\n",
			jail:    true,
		},
		{
			name:    "a warm-up that quietly stopped being one",
			content: "      - name: warm\n        run: go test -race -count=1 ./...\n",
			loose:   []string{"run: go test -race -count=1 ./..."},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			scan := scanWorkflow(tt.content)
			if got := len(scan.jail) > 0; got != tt.jail {
				t.Errorf("found a namespace block = %t, want %t", got, tt.jail)
			}
			if got := len(scan.warmUps); got != tt.warmUps {
				t.Errorf("found %d warm-ups %v, want %d", got, scan.warmUps, tt.warmUps)
			}
			got := make([]string, 0, len(scan.loose))
			for _, loose := range scan.loose {
				got = append(got, loose.text)
			}
			if strings.Join(got, "\n") != strings.Join(tt.loose, "\n") {
				t.Errorf("runs outside the namespace = %q, want %q", got, tt.loose)
			}
		})
	}
}
