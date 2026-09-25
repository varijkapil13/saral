package main

import (
	"flag"
	"fmt"
	"io"
	"slices"
	"strings"
)

var completionShells = []string{"bash", "zsh", "fish"}

func init() {
	registerSubcommand(subcommand{
		name:    "completion",
		summary: "print a shell completion script: completion bash|zsh|fish",
		run:     runCompletion,
	})
	scriptGroups["completion"] = completionShells
}

func runCompletion(inv *invocation, args []string) error {
	if len(args) == 1 && (args[0] == "-h" || args[0] == "--help") {
		_, err := fmt.Fprintf(inv.stdout, "usage: saral completion <%s>\n\n"+
			"  bash: source <(saral completion bash)\n"+
			"  zsh:  source <(saral completion zsh)\n"+
			"  fish: saral completion fish | source\n", strings.Join(completionShells, "|"))
		return err
	}
	if len(args) != 1 {
		return usageErrorf("saral completion takes one shell: %s", strings.Join(completionShells, ", "))
	}
	c := completionModel()
	switch args[0] {
	case "bash":
		return c.bash(inv.stdout)
	case "zsh":
		return c.zsh(inv.stdout)
	case "fish":
		return c.fish(inv.stdout)
	default:
		return usageErrorf("saral completion does not know %q; it writes %s", args[0], strings.Join(completionShells, ", "))
	}
}

type completionFlag struct {
	name, usage string
	takesValue  bool
}

type completions struct {
	top       []string
	rootFlags []completionFlag
	groups    map[string][]string
	leaves    map[string][]completionFlag
	commands  []string
}

func completionModel() completions {
	c := completions{
		top:       append(subcommandNames(), openableViewIDs()...),
		rootFlags: flagsOf(rootFlags(&options{})),
		groups:    scriptGroups,
		leaves:    make(map[string][]completionFlag, len(scriptFlags)),
		commands:  subcommandNames(),
	}
	for path, build := range scriptFlags {
		c.leaves[path] = flagsOf(build())
	}
	return c
}

func flagsOf(fs *flag.FlagSet) []completionFlag {
	var out []completionFlag
	fs.VisitAll(func(f *flag.Flag) {
		if hiddenFlags[f.Name] {
			return
		}
		b, isBool := f.Value.(interface{ IsBoolFlag() bool })
		_, usage := flag.UnquoteUsage(f)
		out = append(out, completionFlag{name: f.Name, usage: usage, takesValue: !isBool || !b.IsBoolFlag()})
	})
	return out
}

func (f completionFlag) spelling() string {
	if len(f.name) == 1 {
		return "-" + f.name
	}
	return "--" + f.name
}

// valued lists the flags whose next word the walk has to skip.
func (c completions) valued() string {
	var names []string
	add := func(flags []completionFlag) {
		for _, f := range flags {
			if f.takesValue && !slices.Contains(names, f.spelling()) {
				names = append(names, f.spelling())
			}
		}
	}
	add(c.rootFlags)
	for _, path := range sortedKeys(c.leaves) {
		add(c.leaves[path])
	}
	slices.Sort(names)
	return strings.Join(names, " ")
}

func flagWords(flags []completionFlag) string {
	words := make([]string, len(flags))
	for i := range flags {
		words[i] = flags[i].spelling()
	}
	return strings.Join(words, " ")
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

const shellWalk = `    local cmd="" sub="" word i valued=" %s "
    for ((i = %s; i < %s; i++)); do
        word="${%s[i]}"
        if [[ "$valued" == *" $word "* ]]; then ((i++)); continue; fi
        [[ "$word" == -* ]] && continue
        if [[ -z "$cmd" ]]; then cmd="$word"; continue; fi
        [[ -z "$sub" ]] && sub="$word"
        break
    done
`

// The specific arms have to come before each command's catch-all.
func (c completions) cases(b *strings.Builder, assign func(words, flags string) string) {
	b.WriteString("    case \"$cmd:$sub\" in\n")
	fmt.Fprintf(b, "        :) %s ;;\n", assign(strings.Join(c.top, " "), flagWords(c.rootFlags)))
	for _, cmd := range c.commands {
		words, grouped := c.groups[cmd]
		if !grouped {
			if flags, ok := c.leaves[cmd]; ok {
				fmt.Fprintf(b, "        %s:*) %s ;;\n", cmd, assign("", flagWords(flags)))
			}
			continue
		}
		for _, sub := range words {
			if flags, ok := c.leaves[cmd+" "+sub]; ok {
				fmt.Fprintf(b, "        %s:%s) %s ;;\n", cmd, sub, assign("", flagWords(flags)))
			}
		}
		fmt.Fprintf(b, "        %s:*) %s ;;\n", cmd, assign(strings.Join(words, " "), ""))
	}
	b.WriteString("    esac\n")
}

func (c completions) bash(w io.Writer) error {
	var b strings.Builder
	b.WriteString("# bash completion for saral. Load it with: source <(saral completion bash)\n")
	b.WriteString("_saral() {\n")
	b.WriteString("    local cur=\"${COMP_WORDS[COMP_CWORD]}\"\n")
	fmt.Fprintf(&b, shellWalk, c.valued(), "1", "COMP_CWORD", "COMP_WORDS")
	b.WriteString("    local words=\"\" flags=\"\"\n")
	c.cases(&b, func(words, flags string) string {
		return fmt.Sprintf("words=%q; flags=%q", words, flags)
	})
	b.WriteString("    [[ \"$cur\" == -* ]] && words=\"$flags\"\n")
	b.WriteString("    [[ -n \"$words\" ]] && COMPREPLY=($(compgen -W \"$words\" -- \"$cur\"))\n")
	b.WriteString("    return 0\n")
	b.WriteString("}\n")
	b.WriteString("complete -o default -F _saral saral\n")
	_, err := io.WriteString(w, b.String())
	return err
}

func (c completions) zsh(w io.Writer) error {
	var b strings.Builder
	b.WriteString("#compdef saral\n")
	b.WriteString("# zsh completion for saral. Load it with: source <(saral completion zsh)\n")
	b.WriteString("_saral() {\n")
	fmt.Fprintf(&b, shellWalk, c.valued(), "2", "CURRENT", "words")
	b.WriteString("    local -a choices flags\n")
	c.cases(&b, func(words, flags string) string {
		return fmt.Sprintf("choices=(%s); flags=(%s)", words, flags)
	})
	b.WriteString("    if [[ \"${words[CURRENT]}\" == -* ]]; then\n")
	b.WriteString("        compadd -- \"${flags[@]}\"\n")
	b.WriteString("    elif (( ${#choices} )); then\n")
	b.WriteString("        compadd -- \"${choices[@]}\"\n")
	b.WriteString("    else\n")
	b.WriteString("        _files\n")
	b.WriteString("    fi\n")
	b.WriteString("}\n")
	b.WriteString("compdef _saral saral\n")
	_, err := io.WriteString(w, b.String())
	return err
}

func (c completions) fish(w io.Writer) error {
	var b strings.Builder
	b.WriteString("# fish completion for saral. Load it with: saral completion fish | source\n")
	b.WriteString("complete -c saral -e\n")
	fmt.Fprintf(&b, "complete -c saral -n %s -f -a %s\n", fishQuote("__fish_use_subcommand"), fishQuote(strings.Join(c.top, " ")))
	for _, f := range c.rootFlags {
		b.WriteString(fishFlag("__fish_use_subcommand", f))
	}
	for _, cmd := range c.commands {
		words, grouped := c.groups[cmd]
		if !grouped {
			for _, f := range c.leaves[cmd] {
				b.WriteString(fishFlag("__fish_seen_subcommand_from "+cmd, f))
			}
			continue
		}
		all := strings.Join(words, " ")
		fmt.Fprintf(&b, "complete -c saral -n %s -f -a %s\n",
			fishQuote("__fish_seen_subcommand_from "+cmd+"; and not __fish_seen_subcommand_from "+all), fishQuote(all))
		for _, sub := range words {
			for _, f := range c.leaves[cmd+" "+sub] {
				b.WriteString(fishFlag("__fish_seen_subcommand_from "+cmd+"; and __fish_seen_subcommand_from "+sub, f))
			}
		}
	}
	_, err := io.WriteString(w, b.String())
	return err
}

func fishFlag(condition string, f completionFlag) string {
	option := " -l "
	if len(f.name) == 1 {
		option = " -s "
	}
	line := "complete -c saral -n " + fishQuote(condition) + option + f.name
	if f.takesValue {
		line += " -r"
	}
	return line + " -d " + fishQuote(f.usage) + "\n"
}

func fishQuote(s string) string {
	return "'" + strings.NewReplacer(`\`, `\\`, `'`, `\'`).Replace(s) + "'"
}
