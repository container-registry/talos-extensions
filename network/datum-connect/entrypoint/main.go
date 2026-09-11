// datum-connect-service is the Talos entrypoint for the Datum Connect tunnel
// agent. datum-connect only authenticates through a credentials helper
// (`datumctl auth get-token --session <name>`), so the service has to establish
// a datumctl service-account session first, then hand over to the agent.
//
// Steps: log in with the mounted service-account credentials file (retried
// until the network is up), read the session name datumctl chose, find an
// existing tunnel for this label so restarts keep their hostname, then exec
// `datum-connect listen`. exec keeps the agent as PID of the container so
// Talos signals reach it directly.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"syscall"
	"time"
)

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "datum-connect-service: "+format+"\n", args...)
	os.Exit(1)
}

func logf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "datum-connect-service: "+format+"\n", args...)
}

type tunnel struct {
	ID     string `json:"id"`
	Label  string `json:"label"`
	Type   string `json:"type"`
	Status string `json:"status"`
}

func main() {
	home := env("HOME", "/var/lib/datum-connect")
	credentials := env("DATUM_CREDENTIALS_FILE", "/usr/local/etc/datum-connect/credentials.json")
	datumctl := env("DATUMCTL_BIN", "/usr/local/bin/datumctl")
	agent := env("DATUM_CONNECT_BIN", "/usr/local/bin/datum-connect")
	project := os.Getenv("DATUM_PROJECT")
	origin := os.Getenv("DATUM_CONNECT_TUNNEL_ORIGIN")
	label := os.Getenv("DATUM_CONNECT_TUNNEL_LABEL")

	if project == "" {
		fail("DATUM_PROJECT is required (set it in the ExtensionServiceConfig environment)")
	}
	if origin == "" {
		fail("DATUM_CONNECT_TUNNEL_ORIGIN is required, e.g. 127.0.0.1:80")
	}
	if label == "" {
		host, err := os.Hostname()
		if err != nil || host == "" {
			host = "talos"
		}
		label = host
	}
	if err := os.MkdirAll(home, 0o700); err != nil {
		fail("create %s: %v", home, err)
	}
	os.Setenv("HOME", home)

	for {
		if _, err := os.Stat(credentials); err == nil {
			break
		}
		logf("waiting for %s (ExtensionServiceConfig configFiles)", credentials)
		time.Sleep(30 * time.Second)
	}

	// Login mints a fresh JWT from the key; running it on every start keeps
	// the on-disk session in sync with the mounted credentials file.
	delay := 5 * time.Second
	for {
		cmd := exec.Command(datumctl, "login", "--credentials", credentials)
		cmd.Stdout = os.Stderr
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err == nil {
			break
		} else {
			logf("datumctl login failed (%v), retrying in %s", err, delay)
		}
		time.Sleep(delay)
		if delay < time.Minute {
			delay *= 2
		}
	}

	session, err := activeSession(home + "/.datumctl/config")
	if err != nil {
		fail("%v", err)
	}
	logf("session %s", session)

	os.Setenv("DATUM_SESSION", session)
	os.Setenv("DATUM_CREDENTIALS_HELPER", datumctl)
	os.Setenv("DATUM_PROJECT", project)
	if os.Getenv("DATUM_CONNECT_DIR") == "" {
		os.Setenv("DATUM_CONNECT_DIR", home+"/connect")
	}

	args := []string{agent, "listen"}
	if id := existingTunnel(agent, label); id != "" {
		logf("reattaching to tunnel %s (label %q)", id, label)
		args = append(args, "--id", id)
	} else {
		logf("creating tunnel %q -> %s", label, origin)
		args = append(args, "--label", label, "--endpoint", origin)
	}
	if err := syscall.Exec(agent, args, os.Environ()); err != nil {
		fail("exec %s: %v", agent, err)
	}
}

var sessionRe = regexp.MustCompile(`(?m)^active-session:\s*(\S+)`)

func activeSession(configPath string) (string, error) {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return "", fmt.Errorf("read datumctl config: %w", err)
	}
	m := sessionRe.FindSubmatch(data)
	if m == nil {
		return "", fmt.Errorf("no active-session in %s", configPath)
	}
	return string(m[1]), nil
}

// existingTunnel returns the id of a live tunnel with the given label, so a
// restart adopts the same HTTPProxy and public hostname instead of creating a
// new one. A failed listing is not fatal: the agent then creates or adopts by
// endpoint on its own.
func existingTunnel(agent, label string) string {
	out, err := exec.Command(agent, "list", "--json").Output()
	if err != nil {
		logf("tunnel listing failed (%v); continuing without --id", err)
		return ""
	}
	var tunnels []tunnel
	if err := json.Unmarshal(out, &tunnels); err != nil {
		logf("tunnel listing unparsable (%v); continuing without --id", err)
		return ""
	}
	for _, t := range tunnels {
		if t.Type == "tunnel" && t.Label == label {
			return t.ID
		}
	}
	return ""
}
