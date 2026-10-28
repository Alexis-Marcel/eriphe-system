package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"golang.org/x/term"
)

// loginCommand asks for the API token, checks it and saves it.
func loginCommand(ctx context.Context, args []string) error {
	if len(args) != 1 {
		return errors.New("usage: eriphe login <url>")
	}
	url := strings.TrimRight(args[0], "/")
	if !strings.Contains(url, "://") {
		url = "http://" + url
	}
	token, err := readToken()
	if err != nil {
		return err
	}
	if _, err := NewClient(url, token).Messages(ctx, 0, 0); err != nil {
		return fmt.Errorf("cannot reach eriphe-core with this token: %w", err)
	}
	path, err := saveConfig(&Config{URL: url, Token: token})
	if err != nil {
		return err
	}
	fmt.Printf("Logged in to %s (saved in %s).\n", url, path)
	return nil
}

// readToken reads the token without echo from a terminal, or a line from a pipe.
func readToken() (string, error) {
	fd := int(os.Stdin.Fd())
	if term.IsTerminal(fd) {
		fmt.Fprint(os.Stderr, "API token: ")
		raw, err := term.ReadPassword(fd)
		fmt.Fprintln(os.Stderr)
		if err != nil {
			return "", err
		}
		return requireToken(string(raw))
	}
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && err != io.EOF {
		return "", err
	}
	return requireToken(line)
}

func requireToken(s string) (string, error) {
	if s = strings.TrimSpace(s); s == "" {
		return "", errors.New("empty token")
	}
	return s, nil
}

// sayCommand sends a message and prints what follows in the conversation
// until Eriphe has replied and gone quiet, or -wait seconds.
func sayCommand(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("say", flag.ContinueOnError)
	wait := fs.Int("wait", 120, "seconds to wait for the replies")
	if err := fs.Parse(args); err != nil {
		return err
	}
	text := strings.TrimSpace(strings.Join(fs.Args(), " "))
	if text == "" {
		return errors.New(`usage: eriphe say [-wait 120] "text"`)
	}
	c, err := client()
	if err != nil {
		return err
	}
	return say(ctx, c, os.Stdout, text, time.Duration(*wait)*time.Second, 5*time.Second)
}

// say is sayCommand once configured: after the first reply of the brain,
// it stops when nothing new came for quiet.
func say(ctx context.Context, c *Client, w io.Writer, text string, wait, quiet time.Duration) error {
	after, err := c.Send(ctx, text)
	if err != nil {
		return err
	}
	deadline := time.Now().Add(wait)
	replied := false
	for {
		left := time.Until(deadline)
		if replied {
			left = min(left, quiet)
		}
		if left <= 0 {
			break
		}
		msgs, err := c.Messages(ctx, after, max(1, int(left.Round(time.Second)/time.Second)))
		if err != nil {
			return err
		}
		if len(msgs) == 0 && replied {
			break
		}
		for _, m := range msgs {
			after = m.Seq
			fmt.Fprintln(w, plainMessage(m))
			replied = replied || m.Role == "brain"
		}
	}
	if !replied {
		return errors.New("no reply yet: Eriphe may be busy, see the conversation in `eriphe`")
	}
	return nil
}

// plainMessage formats a conversation message for a script.
func plainMessage(m Message) string {
	if m.Role == "brain" {
		return m.Text
	}
	return fmt.Sprintf("[%s] %s", roleLabel(m.Role), m.Text)
}

func tasksCommand(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("tasks", flag.ContinueOnError)
	all := fs.Bool("all", false, "include finished tasks")
	if err := fs.Parse(args); err != nil {
		return err
	}
	c, err := client()
	if err != nil {
		return err
	}
	tasks, err := c.Tasks(ctx, *all)
	if err != nil {
		return err
	}
	printTasks(os.Stdout, tasks, time.Now())
	return nil
}

// printTasks writes tasks as an aligned table.
func printTasks(w io.Writer, tasks []Task, now time.Time) {
	if len(tasks) == 0 {
		fmt.Fprintln(w, "No tasks.")
		return
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tSTATE\tNODE\tAGENT\tDIR\tCREATED")
	for _, t := range tasks {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", t.ID, t.State, t.Node, orDash(t.Agent), shortDir(t.Dir, 40), clock(t.Created, now))
	}
	tw.Flush()
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func attachCommand(ctx context.Context, args []string) error {
	if len(args) != 1 {
		return errors.New("usage: eriphe attach <id>")
	}
	c, err := client()
	if err != nil {
		return err
	}
	return attach(ctx, c, os.Stdout, args[0])
}

// attach prints a task's recent events, then the new ones as they come,
// until the task ends or ctx is done. A lost connection is retried.
func attach(ctx context.Context, c *Client, w io.Writer, id string) error {
	st, err := c.Task(ctx, id, 20)
	if err != nil {
		return err
	}
	t := st.Task
	fmt.Fprintf(w, "%s on %s (%s), %s: %s\n", t.ID, t.Node, orDash(t.Agent), t.State, truncate(oneLine(t.Prompt), 200))
	after := 0
	for _, e := range st.Recent {
		fmt.Fprintln(w, plainEvent(e))
		after = e.Seq
	}
	backoff := time.Second
	for ctx.Err() == nil {
		events, err := c.Events(ctx, id, after, 60)
		if err != nil {
			if ctx.Err() != nil {
				break
			}
			var api *APIError
			if errors.As(err, &api) {
				return err
			}
			fmt.Fprintf(os.Stderr, "eriphe: %v, retrying in %s\n", err, backoff)
			sleep(ctx, backoff)
			backoff = min(backoff*2, 30*time.Second)
			continue
		}
		backoff = time.Second
		for _, e := range events {
			fmt.Fprintln(w, plainEvent(e))
			after = e.Seq
		}
		if len(events) == 0 {
			if st, err := c.Task(ctx, id, 0); err == nil && finished(st.Task.State) {
				fmt.Fprintf(w, "Task %s is %s.\n", id, st.Task.State)
				return nil
			}
		}
	}
	return ctx.Err()
}

// plainEvent formats a task event on one or more lines.
func plainEvent(e Event) string {
	label, text := eventText(e)
	return fmt.Sprintf("%s %-10s %s", e.Time.Local().Format("15:04:05"), label, text)
}

func sleep(ctx context.Context, d time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}
