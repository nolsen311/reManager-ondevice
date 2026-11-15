//go:build linux

// remanager-ondevice is the backend of reManager's on-tablet build.
//
// AppLoad starts it with a socket path as its only argument. Run by hand with a
// subcommand, it does the same work from a shell, which is how the backend is
// tried on a tablet before there is a UI.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"reManager/internal/appload"
	"reManager/internal/executor"
	"reManager/internal/ondevice"
	"reManager/internal/vellum"
)

const usage = `usage:
  remanager-ondevice <socket>             serve the AppLoad frontend (how AppLoad starts it)
  remanager-ondevice list [query]         list packages in the catalogue
  remanager-ondevice installed            list installed packages
  remanager-ondevice info <package>       show one package
  remanager-ondevice plan <package>...    show what an install would do, changing nothing
  remanager-ondevice install <package>... install, in this process
  remanager-ondevice plan-remove <package>...   show what a removal would do, changing nothing
  remanager-ondevice remove <package>...  remove, in this process
  remanager-ondevice upgrades             list available upgrades (refreshes Vellum's index)
  remanager-ondevice plan-upgrade [package]...  show what an upgrade would do, changing nothing
  remanager-ondevice upgrade [package]... upgrade the named packages, or everything
  remanager-ondevice job                  show the last job started from the app

options, before the subcommand:
  --allow-untested   run on a tablet model this build has not been tried on
`

func main() {
	allowUntested := flag.Bool("allow-untested", false, "")
	flag.Usage = func() { fmt.Fprint(os.Stderr, usage) }
	flag.Parse()
	args := flag.Args()
	if len(args) == 0 {
		flag.Usage()
		os.Exit(2)
	}
	if err := run(args, *allowUntested); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(args []string, allowUntested bool) error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	jobs := &ondevice.Jobs{}
	if args[0] == "job" {
		status, err := jobs.Status()
		if err != nil {
			return err
		}
		return printJSON(status)
	}

	device, err := ondevice.DetectDevice()
	if err != nil {
		return err
	}
	if !device.Tested() && !allowUntested {
		return fmt.Errorf("this build has only been tried on the Paper Pro Move, and this is a %s; pass --allow-untested to run anyway", device.Type.DisplayName())
	}

	metadata := vellum.NewMetadataStore()
	exec := executor.NewLocalExecutor(ctx)
	service := ondevice.NewService(metadata, metadata, exec, device)

	switch args[0] {
	case "list", "installed":
		req := ondevice.ListRequest{InstalledOnly: args[0] == "installed", Query: strings.Join(args[1:], " "), IncludeIncompatible: true}
		for {
			page, err := service.List(req)
			if err != nil {
				return err
			}
			for _, pkg := range page.Packages {
				mark := " "
				if pkg.Installed {
					mark = "*"
				}
				fmt.Printf("%s %-36s %-14s %s\n", mark, pkg.Name, pkg.Version, pkg.Description)
			}
			req.Offset = page.Offset + len(page.Packages)
			if len(page.Packages) == 0 || req.Offset >= page.Total {
				return nil
			}
		}
	case "info":
		if len(args) != 2 {
			return fmt.Errorf("info takes one package name")
		}
		detail, err := service.Detail(args[1])
		if err != nil {
			return err
		}
		return printJSON(detail)
	case "plan":
		plan, err := service.Plan(args[1:])
		if err != nil {
			return err
		}
		return printJSON(plan)
	case "install":
		return reportInstall(service.Install(args[1:], printProgress))
	case "plan-remove":
		plan, err := service.PlanRemove(args[1:])
		if err != nil {
			return err
		}
		return printJSON(plan)
	case "remove":
		return reportInstall(service.Remove(args[1:], printProgress))
	case "upgrades":
		upgrades, err := service.Upgrades()
		if err != nil {
			return err
		}
		for _, u := range upgrades {
			fmt.Printf("%-36s %s -> %s\n", u.Name, u.From, u.To)
		}
		return nil
	case "plan-upgrade":
		plan, err := service.PlanUpgrade(args[1:])
		if err != nil {
			return err
		}
		return printJSON(plan)
	case "upgrade":
		return reportInstall(service.Upgrade(args[1:], printProgress))
	case "worker":
		return runWorker(args[1:], service)
	default:
		if strings.HasPrefix(args[0], "-") || len(args) != 1 {
			return fmt.Errorf("unknown command %q", args[0])
		}
		conn, err := appload.Dial(args[0])
		if err != nil {
			return err
		}
		defer conn.Close()
		handler := &ondevice.Handler{Service: service, Jobs: jobs}
		return handler.Serve(ctx, conn)
	}
}

// runWorker is what a job started from the app runs as, in its own systemd unit: worker <action> --state <dir> -- [package]...
func runWorker(args []string, service *ondevice.Service) error {
	if len(args) < 4 || args[1] != "--state" || args[3] != "--" {
		return fmt.Errorf("worker is started by the app, not by hand")
	}
	run := map[string]func([]string, func(ondevice.Progress)) ondevice.InstallResult{
		ondevice.ActionInstall: service.Install,
		ondevice.ActionRemove:  service.Remove,
		ondevice.ActionUpgrade: service.Upgrade,
		// Only from the worker: these restart the UI the app is drawn in, and from a shell over SSH desktop reManager or xovi's own scripts do the same.
		ondevice.ActionRestart:          service.Restart,
		ondevice.ActionRebuildHashtable: service.RebuildHashtable,
	}[args[0]]
	if run == nil {
		return fmt.Errorf("unknown worker action %q", args[0])
	}
	jobs := &ondevice.Jobs{Dir: args[2]}
	return jobs.RunWorker(args[0], args[4:], run)
}

func printProgress(p ondevice.Progress) {
	if p.Message != "" {
		fmt.Println(p.Message)
	}
}

func reportInstall(result ondevice.InstallResult) error {
	for _, step := range result.Deferred {
		fmt.Println("not run, do it later:", step)
	}
	if !result.Success {
		return fmt.Errorf("%s", strings.Join(result.Errors, "; "))
	}
	fmt.Println("done")
	return nil
}

func printJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
