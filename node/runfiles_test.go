package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// No secret and no prompt on a task's command line, whatever its runtime
// and engine: every user of the machine reads it (ps, docker inspect). Claude
// Code's go to its run files, 0600 in a 0700 directory, mounted read-only
// and deleted with the container.
func TestTaskCommandLineHasNoSecrets(t *testing.T) {
	for _, engine := range []string{engineDocker, engineContainer} {
		for _, rt := range []string{"claude-code", "opencode", "codex", "acp"} {
			t.Run(engine+"/"+rt, func(t *testing.T) { testNoSecrets(t, engine, rt) })
		}
	}
}

func testNoSecrets(t *testing.T, engine, rt string) {
	if filepath.Separator != '/' {
		t.Skip("needs sh")
	}
	var cli, argsFile string
	if engine == engineContainer {
		var dir string
		cli, dir = fakeContainer(t)
		argsFile = filepath.Join(dir, "run")
	} else {
		dir := t.TempDir()
		argsFile = filepath.Join(dir, "args")
		cli = filepath.Join(dir, "docker")
		script := "#!/bin/sh\nif [ \"$1\" = network ]; then echo \"172.17.0.0/16 172.17.0.1;\"; exit; fi\nprintf '%s\\n' \"$@\" > " + argsFile + "\ncat >/dev/null\n"
		if err := os.WriteFile(cli, []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	root := filepath.Join(t.TempDir(), "root")
	n := newNode(options{root: root, hostRoot: root, docker: cli, engine: engine, taskNetwork: "eriphe-tasks",
		callbackURL: "http://eriphe-node:8790", listenAddr: "0.0.0.0:8790", egressPort: "8791"})
	const prompt = "Standing instructions of the brain, with the memory profile."
	task := mcpTask()
	task.secret = "task-secret-1234"
	task.dir = filepath.Join(root, "tasks", task.ID)
	task.Spec.Runtime = rt
	task.Spec.Image = "worker"
	task.Spec.AppendSystemPrompt = prompt
	task.Spec.Identity = map[string]string{"TOK": "tok-1", "CLAUDE_CODE_OAUTH_TOKEN": "oauth-token-5678", "GH_TOKEN": "gh-token-9012"}
	if rt == "acp" {
		task.Spec.Command = []string{"goose", "acp"}
	}
	if err := n.startContainer(task, "go"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { _, err := os.Stat(argsFile); return err == nil })
	raw, _ := os.ReadFile(argsFile)
	args := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if engine == engineContainer {
		args = args[:len(args)-3] // the fake's own record of TZ, HTTPS_PROXY and NO_PROXY
	}
	line := strings.Join(args, "\n")
	for _, secret := range []string{task.secret, "Authorization", "Bearer", "core-token", "tok-1",
		"oauth-token-5678", "gh-token-9012", prompt} {
		if strings.Contains(line, secret) {
			t.Errorf("%q on the command line: %v", secret, args)
		}
	}
	for i, a := range args {
		if a == "-e" && i+1 < len(args) && strings.Contains(args[i+1], "=") {
			t.Errorf("a variable's value on the command line: %s", args[i+1])
		}
	}

	dir := runDir(task)
	if rt == "claude-code" {
		mount := mountSpec("type=bind", "src="+dir, "dst="+runMount, "readonly")
		if engine == engineContainer {
			mount = "type=bind,source=" + dir + ",target=" + runMount + ",readonly"
		}
		if !hasSeq(args, "--mount", mount) {
			t.Errorf("run files not mounted read-only (%s): %v", mount, args)
		}
		managed := filepath.Join(dir, claudeManaged)
		mount = mountSpec("type=bind", "src="+managed, "dst="+claudeManagedMount, "readonly")
		if engine == engineContainer {
			mount = "type=bind,source=" + managed + ",target=" + claudeManagedMount + ",readonly"
		}
		if !hasSeq(args, "--mount", mount) {
			t.Errorf("managed settings not mounted read-only (%s): %v", mount, args)
		}
		for _, want := range [][]string{{"--mcp-config", runPath(claudeMCP)}, {"--settings", runPath(claudeSettings)},
			{"--append-system-prompt-file", runPath(claudePrompt)}} {
			if !hasSeq(args, want...) {
				t.Errorf("missing %v in %v", want, args)
			}
		}
		if fi, err := os.Stat(dir); err != nil || runtime.GOOS != "windows" && fi.Mode().Perm() != 0o700 {
			t.Errorf("run directory: %v, %v", fi, err)
		}
		for name, want := range map[string]string{claudeMCP: "Bearer " + task.secret, claudeSettings: "attribution", claudePrompt: prompt} {
			p := filepath.Join(dir, name)
			fi, err := os.Stat(p)
			if err != nil {
				t.Errorf("%s: %v", name, err)
				continue
			}
			if fi.Mode().Perm() != 0o600 {
				t.Errorf("%s: mode %v, want 0600", name, fi.Mode().Perm())
			}
			if b, _ := os.ReadFile(p); !strings.Contains(string(b), want) {
				t.Errorf("%s: %s", name, b)
			}
		}
	} else if _, err := os.Stat(dir); err == nil {
		t.Errorf("run files for %s, which needs none", rt)
	}

	task.run.stop()
	waitFor(t, func() bool { return finished(task.state()) })
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("run files left after the task: %v", err)
	}
}

// A run's files replace the last run's, and a bad name is refused.
func TestWriteRunFiles(t *testing.T) {
	task := &Task{dir: t.TempDir()}
	if err := writeRunFiles(task, map[string][]byte{"old": []byte("x")}); err != nil {
		t.Fatal(err)
	}
	if err := writeRunFiles(task, map[string][]byte{"new": []byte("y")}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(runDir(task), "old")); !os.IsNotExist(err) {
		t.Error("an earlier run's file is left")
	}
	for _, bad := range []string{"../x", "a/b/c", "a/..", "/x", "a/", "..", "."} {
		if err := writeRunFiles(task, map[string][]byte{bad: nil}); err == nil {
			t.Errorf("%q written", bad)
		}
	}
	// One subdirectory, of the node's own.
	if err := writeRunFiles(task, map[string][]byte{"sub/f": []byte("z")}); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(filepath.Join(runDir(task), "sub")); err != nil || runtime.GOOS != "windows" && fi.Mode().Perm() != 0o700 {
		t.Errorf("subdirectory: %v, %v", fi, err)
	}
	if err := writeRunFiles(task, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(runDir(task)); !os.IsNotExist(err) {
		t.Error("an empty run directory")
	}
}
