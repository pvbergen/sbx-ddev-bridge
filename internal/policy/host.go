package policy

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

// ddevGlobalDir returns ddev's global config directory on the host.
func ddevGlobalDir() string {
	if d := os.Getenv("DDEV_GLOBAL_DIR"); d != "" {
		return d
	}
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		if _, err := os.Stat(filepath.Join(x, "ddev")); err == nil {
			return filepath.Join(x, "ddev")
		}
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".ddev")
}

// OtherProjects returns the names of the ddev projects known on this host
// (from ddev's global project_list.yaml), excluding self.
func OtherProjects(self string) []string {
	f, err := os.Open(filepath.Join(ddevGlobalDir(), "project_list.yaml"))
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if line == "" || line[0] == ' ' || line[0] == '\t' || line[0] == '#' || !strings.HasSuffix(strings.TrimSpace(line), ":") {
			continue
		}
		name := strings.Trim(strings.TrimSuffix(strings.TrimSpace(line), ":"), `"'`)
		if name != "" && name != self {
			out = append(out, name)
		}
	}
	return out
}

// CustomCommands lists the custom ddev commands available to the project at
// root: .ddev/commands/<service>/<name> plus the global ~/.ddev/commands, and
// any "## Aliases:" they declare.
func CustomCommands(root string) []string {
	seen := map[string]bool{}
	var out []string
	for _, dir := range []string{filepath.Join(root, ".ddev", "commands"), filepath.Join(ddevGlobalDir(), "commands")} {
		services, _ := os.ReadDir(dir)
		for _, svc := range services {
			if !svc.IsDir() || strings.HasPrefix(svc.Name(), ".") {
				continue
			}
			files, _ := os.ReadDir(filepath.Join(dir, svc.Name()))
			for _, f := range files {
				n := f.Name()
				if f.IsDir() || strings.HasPrefix(n, ".") || strings.Contains(n, ".example") || strings.HasPrefix(n, "README") {
					continue
				}
				names := []string{n}
				names = append(names, aliasesOf(filepath.Join(dir, svc.Name(), n))...)
				for _, x := range names {
					if !seen[x] {
						seen[x] = true
						out = append(out, x)
					}
				}
			}
		}
	}
	return out
}

func aliasesOf(file string) []string {
	f, err := os.Open(file)
	if err != nil {
		return nil
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for n := 0; sc.Scan() && n < 50; n++ {
		line := strings.TrimSpace(sc.Text())
		if v, ok := strings.CutPrefix(line, "## Aliases:"); ok {
			var out []string
			for _, a := range strings.Split(v, ",") {
				if a = strings.TrimSpace(a); a != "" {
					out = append(out, a)
				}
			}
			return out
		}
	}
	return nil
}
