package policy

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// DrushCommands are the commands the project's drush knows, read from
// `drush list --format=json` (Symfony's JSON descriptor, hidden commands included).
type DrushCommands struct {
	names map[string]string // command name or alias -> command name
}

// DrushLister returns the project's drush commands. refresh asks for a new
// listing instead of a cached one, after a name was not found.
type DrushLister func(refresh bool) (*DrushCommands, error)

// ParseDrushList reads the output of `drush list --format=json`. Symfony puts
// a command's aliases into its "usage" list, next to the synopsis and the usage
// examples; those contain spaces, aliases don't.
func ParseDrushList(out []byte) (*DrushCommands, error) {
	if i := bytes.IndexByte(out, '{'); i > 0 {
		out = out[i:] // skip anything ddev printed before the JSON
	}
	var l struct {
		Commands []struct {
			Name  string   `json:"name"`
			Usage []string `json:"usage"`
		} `json:"commands"`
	}
	if err := json.Unmarshal(out, &l); err != nil {
		return nil, fmt.Errorf("unexpected `drush list --format=json` output: %v", err)
	}
	c := &DrushCommands{names: map[string]string{}}
	for _, cmd := range l.Commands {
		if cmd.Name != "" {
			c.names[cmd.Name] = cmd.Name
		}
	}
	if len(c.names) == 0 {
		return nil, errors.New("`drush list --format=json` listed no commands")
	}
	for _, cmd := range l.Commands {
		for _, u := range cmd.Usage {
			if _, taken := c.names[u]; !taken && u != "" && !strings.ContainsAny(u, " \t") {
				c.names[u] = cmd.Name
			}
		}
	}
	return c, nil
}

// ErrDrushNotListed is returned by a DrushLister that may not list commands
// now (the bridge's early check, before the project lock). The name is then
// left to the check that runs before the command.
var ErrDrushNotListed = errors.New("drush commands not listed yet")

var (
	errDrushUnknown   = errors.New("unknown")
	errDrushAmbiguous = errors.New("ambiguous")
)

// Resolve finds the command drush would run for name, the way Symfony
// Console's Application::find does: an exact name or alias first, then an
// abbreviation where every ":"-separated part is a prefix (case-sensitive,
// then case-insensitive). Unlike Symfony it never picks among several
// commands; an ambiguous name is an error.
func (c *DrushCommands) Resolve(name string) (string, error) {
	if n, ok := c.names[name]; ok {
		return n, nil
	}
	parts := strings.Split(name, ":")
	for i, p := range parts {
		parts[i] = regexp.QuoteMeta(p)
	}
	expr := strings.Join(parts, "[^:]*:") + "[^:]*"
	prefix := regexp.MustCompile("^" + expr)
	full := regexp.MustCompile("(?i)^" + expr + "$")

	matches := c.match(prefix)
	if len(matches) == 0 {
		matches = c.match(regexp.MustCompile("(?i)^" + expr))
	}
	whole := false
	for _, m := range matches {
		whole = whole || full.MatchString(m)
	}
	if !whole {
		return "", errDrushUnknown
	}
	commands := map[string]bool{}
	for _, m := range matches {
		commands[c.names[m]] = true
	}
	if len(commands) > 1 {
		var list []string
		for n := range commands {
			list = append(list, n)
		}
		sort.Strings(list)
		if len(list) > 5 {
			list = append(list[:5], "…")
		}
		return "", fmt.Errorf("%w: %s", errDrushAmbiguous, strings.Join(list, ", "))
	}
	for n := range commands {
		return n, nil
	}
	return "", errDrushUnknown
}

func (c *DrushCommands) match(re *regexp.Regexp) []string {
	var out []string
	for n := range c.names {
		if re.MatchString(n) {
			out = append(out, n)
		}
	}
	return out
}

// resolveDrush maps the command an agent typed to the command drush runs,
// listing the project's commands again once if the name is not found.
func resolveDrush(p Project, name string) (string, Decision) {
	if p.Drush == nil {
		return "", deny("drush commands cannot be checked: the bridge has no command list for this project")
	}
	var canonical string
	var err error
	for _, refresh := range []bool{false, true} {
		cmds, lerr := p.Drush(refresh)
		if errors.Is(lerr, ErrDrushNotListed) {
			return name, allow(nil)
		}
		if lerr != nil {
			return "", deny("could not list this project's drush commands (%v); is the project running? Start it with `ddev start`", lerr)
		}
		if canonical, err = cmds.Resolve(name); !errors.Is(err, errDrushUnknown) {
			break
		}
	}
	switch {
	case errors.Is(err, errDrushAmbiguous):
		return "", deny("drush %s is ambiguous (%v); use the full command name", name, strings.TrimPrefix(err.Error(), errDrushAmbiguous.Error()+": "))
	case err != nil:
		return "", deny("drush %s is not a command of this project's drush; use a full command name from `drush list`", name)
	}
	return canonical, allow(nil)
}
