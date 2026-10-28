// eriphe is the terminal client of eriphe-core: talk to Eriphe (the brain),
// follow its worker tasks and answer them.
//
//	eriphe login <url>        # save eriphe-core's URL and API token
//	eriphe                    # the terminal UI
//	eriphe say "text"         # send a message, print the replies
//	eriphe tasks              # list tasks
//	eriphe attach <id>        # follow a task's events
//	eriphe servers            # the tool catalog and its status
//	eriphe connect <server>   # authorize Eriphe to use an OAuth server
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
)

const usage = `eriphe: talk to Eriphe and follow its tasks.

Usage:
  eriphe                       open the terminal UI
  eriphe login <url>           save eriphe-core's URL and API token
  eriphe say [-wait 120] text  send a message to Eriphe and print its replies
  eriphe tasks [-all]          list tasks (active ones unless -all)
  eriphe attach <id>           print a task's events as they come (Ctrl-C to quit)
  eriphe servers               list the tool catalog: MCP servers, status, tools
  eriphe connect [-no-browser] <server>
                               authorize Eriphe to use a server that needs OAuth

ERIPHE_URL and ERIPHE_TOKEN override the saved configuration.
`

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	args := os.Args[1:]
	cmd := ""
	if len(args) > 0 {
		cmd, args = args[0], args[1:]
	}
	var err error
	switch cmd {
	case "":
		err = runTUI()
	case "login":
		err = loginCommand(ctx, args)
	case "say":
		err = sayCommand(ctx, args)
	case "tasks":
		err = tasksCommand(ctx, args)
	case "attach":
		err = attachCommand(ctx, args)
	case "servers":
		err = serversCommand(ctx, args)
	case "connect":
		err = connectCommand(ctx, args)
	case "help", "-h", "-help", "--help":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "eriphe: unknown command %q\n\n%s", cmd, usage)
		os.Exit(2)
	}
	if err != nil && ctx.Err() == nil {
		fmt.Fprintln(os.Stderr, "eriphe:", err)
		os.Exit(1)
	}
}

// client builds a client from the saved configuration and the environment.
func client() (*Client, error) {
	cfg, err := loadConfig()
	if err != nil {
		return nil, err
	}
	return NewClient(cfg.URL, cfg.Token), nil
}
