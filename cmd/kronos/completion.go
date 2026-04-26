package main

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"
)

func runCompletion(ctx context.Context, out io.Writer, args []string) error {
	_ = ctx
	if len(args) == 0 {
		return fmt.Errorf("completion shell is required")
	}
	commands := completionCommands(registry())
	flags := completionGlobalFlags()
	subcommands := completionSubcommands()
	switch args[0] {
	case "bash":
		return writeBashCompletion(out, commands, flags, subcommands)
	case "zsh":
		return writeZshCompletion(out, commands, flags, subcommands)
	case "fish":
		return writeFishCompletion(out, commands, flags, subcommands)
	default:
		return fmt.Errorf("unsupported completion shell %q", args[0])
	}
}

func completionCommands(commands map[string]command) []string {
	names := make([]string, 0, len(commands)+1)
	names = append(names, "help")
	for name := range commands {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func completionGlobalFlags() []string {
	return []string{"--no-color", "--output", "--request-id", "--server", "--token"}
}

func completionSubcommands() map[string][]string {
	subcommands := map[string][]string{
		"agent":        {"inspect", "list"},
		"audit":        {"list", "search", "tail", "verify"},
		"backup":       {"inspect", "list", "now", "protect", "unprotect", "verify"},
		"completion":   {"bash", "fish", "zsh"},
		"config":       {"inspect", "validate"},
		"jobs":         {"cancel", "inspect", "list", "retry"},
		"key":          {"add-slot", "escrow", "list", "remove-slot", "rotate"},
		"notification": {"add", "inspect", "list", "remove", "update"},
		"restore":      {"preview", "start"},
		"retention":    {"apply", "plan", "policy"},
		"schedule":     {"add", "inspect", "list", "pause", "remove", "resume", "tick", "update"},
		"storage":      {"add", "du", "inspect", "list", "remove", "test", "update"},
		"target":       {"add", "inspect", "list", "remove", "test", "update"},
		"token":        {"create", "inspect", "list", "prune", "revoke", "verify"},
		"user":         {"add", "grant", "inspect", "list", "remove"},
	}
	for _, names := range subcommands {
		sort.Strings(names)
	}
	return subcommands
}

func writeBashCompletion(out io.Writer, commands []string, flags []string, subcommands map[string][]string) error {
	_, err := fmt.Fprintf(out, `_kronos_completion() {
  local cur="${COMP_WORDS[COMP_CWORD]}"
  local commands="%s"
  local global_flags="%s"
  if [[ ${COMP_CWORD} -eq 1 ]]; then
    COMPREPLY=( $(compgen -W "${commands} ${global_flags}" -- "${cur}") )
  elif [[ ${COMP_CWORD} -eq 2 ]]; then
    case "${COMP_WORDS[1]}" in
%s
    esac
  fi
}
complete -F _kronos_completion kronos
`, strings.Join(commands, " "), strings.Join(flags, " "), bashSubcommandCases(subcommands))
	return err
}

func writeZshCompletion(out io.Writer, commands []string, flags []string, subcommands map[string][]string) error {
	_, err := fmt.Fprintf(out, `#compdef kronos
_kronos() {
  local -a commands global_flags subcommands
  commands=(%s)
  global_flags=(%s)
  if (( CURRENT == 2 )); then
    _describe 'global flag' global_flags
    _describe 'command' commands
  elif (( CURRENT == 3 )); then
    case "${words[2]}" in
%s
    esac
  fi
}
_kronos "$@"
`, strings.Join(quoteZshWords(commands), " "), strings.Join(quoteZshWords(flags), " "), zshSubcommandCases(subcommands))
	return err
}

func writeFishCompletion(out io.Writer, commands []string, flags []string, subcommands map[string][]string) error {
	for _, name := range commands {
		if _, err := fmt.Fprintf(out, "complete -c kronos -f -n '__fish_is_first_token' -a %s\n", shellQuote(name)); err != nil {
			return err
		}
	}
	for _, flag := range flags {
		if _, err := fmt.Fprintf(out, "complete -c kronos -f -n '__fish_is_first_token' -l %s\n", strings.TrimPrefix(flag, "--")); err != nil {
			return err
		}
	}
	for _, command := range sortedMapKeys(subcommands) {
		if _, err := fmt.Fprintf(out, "complete -c kronos -f -n '__fish_seen_subcommand_from %s' -a %s\n", shellQuote(command), shellQuote(strings.Join(subcommands[command], " "))); err != nil {
			return err
		}
	}
	return nil
}

func bashSubcommandCases(subcommands map[string][]string) string {
	var lines []string
	for _, command := range sortedMapKeys(subcommands) {
		lines = append(lines, fmt.Sprintf("      %s) COMPREPLY=( $(compgen -W %s -- \"${cur}\") ) ;;", command, shellQuote(strings.Join(subcommands[command], " "))))
	}
	return strings.Join(lines, "\n")
}

func zshSubcommandCases(subcommands map[string][]string) string {
	var lines []string
	for _, command := range sortedMapKeys(subcommands) {
		lines = append(lines, fmt.Sprintf("      %s) subcommands=(%s); _describe 'subcommand' subcommands ;;", command, strings.Join(quoteZshWords(subcommands[command]), " ")))
	}
	return strings.Join(lines, "\n")
}

func sortedMapKeys(values map[string][]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func quoteZshWords(words []string) []string {
	out := make([]string, 0, len(words))
	for _, word := range words {
		out = append(out, shellQuote(word))
	}
	return out
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}
