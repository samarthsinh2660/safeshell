package rules

import (
	"slices"
	"strings"

	"github.com/samarthsinh2660/safeshell/internal/shellparse"
)

// dockerReads lists the read-only subcommands of docker's management commands;
// they match the short forms (ps, images, inspect, ...) that are already R0.
var dockerReads = map[string][]string{
	"container": {"ls", "list", "ps", "inspect", "logs", "top", "port", "stats", "diff"},
	"image":     {"ls", "list", "inspect", "history"},
	"volume":    {"ls", "list", "inspect"},
	"network":   {"ls", "list", "inspect"},
	"context":   {"ls", "inspect"},
	"system":    {"df", "info"},
	"compose":   {"ps", "ls", "config", "images", "logs"},
}

func ruleDocker(c shellparse.Command, f flags) []Finding {
	if len(f.pos) > 1 && slices.Contains(dockerReads[f.pos[0]], f.pos[1]) {
		return one(fnd(R0, c, "Only reads container information."))
	}
	switch f.sub() {
	case "ps", "images", "logs", "inspect", "info", "version", "stats", "top", "history", "ls", "search", "diff", "port":
		return one(fnd(R0, c, "Only reads container information."))
	case "compose":
		if len(f.pos) > 1 && f.pos[1] == "down" && f.has('v', "volumes") {
			return one(fnd(R3, c, "Stops the project and deletes its volumes, which hold the data of databases and similar services."))
		}
		return one(fnd(R2, c, "Starts, stops or rebuilds a project's containers."))
	case "down":
		if c.Name == "docker-compose" {
			if f.has('v', "volumes") {
				return one(fnd(R3, c, "Stops the project and deletes its volumes, which hold the data of databases and similar services."))
			}
			return one(fnd(R2, c, "Stops and removes the project's containers."))
		}
	case "stop", "kill", "restart", "pause":
		return one(fnd(R2, c, "Stops running containers and the services in them."))
	case "rm", "rmi":
		return one(fnd(R2, c, "Removes containers or images. Data inside containers is lost."))
	case "run":
		if f.hasAny("--privileged") || strings.Contains(f.joined(), "-v /:/") || strings.Contains(f.joined(), "--volume /:/") {
			return one(fnd(R3, c, "Gives the container root-level access to the host machine."))
		}
		return one(fnd(R1, c, "Starts a container."))
	case "system", "volume", "network", "container", "image", "builder":
		if len(f.pos) > 1 && (f.pos[1] == "prune" || f.pos[1] == "rm") {
			if c.Name != "" && (f.pos[0] == "volume" || f.hasAny("--volumes") || f.hasAny("-a", "-af", "--all")) {
				return one(fnd(R3, c, "Permanently deletes unused containers, images or volumes. Volume data is lost."))
			}
			return one(fnd(R2, c, "Removes unused Docker objects."))
		}
	}
	return one(fnd(R1, c, "Runs a container command."))
}

func ruleKubectl(c shellparse.Command, f flags) []Finding {
	switch f.sub() {
	case "get", "describe", "logs", "diff", "top", "explain", "version", "config", "api-resources", "events", "cluster-info":
		return one(fnd(R0, c, "Only reads cluster information."))
	case "delete":
		if f.hasAny("--all", "-A", "--all-namespaces") || (len(f.pos) > 1 && (f.pos[1] == "namespace" || f.pos[1] == "ns" || f.pos[1] == "namespaces")) {
			return one(fnd(R3, c, "Deletes a whole namespace or every object of a kind. This can take down production services.").
				WithAlt(strings.Replace(c.String(), "delete", "delete --dry-run=server", 1), "asks the server what would be deleted, without deleting"))
		}
		if len(f.pos) > 2 && (f.pos[1] == "pod" || f.pos[1] == "pods" || f.pos[1] == "po") {
			return one(fnd(R2, c, "Deletes one pod. Its controller normally starts a replacement, but anything stored only inside it is lost."))
		}
		return one(fnd(R3, c, "Deletes cluster objects. Running services stop and their data may be lost.").
			WithAlt(strings.Replace(c.String(), "delete", "delete --dry-run=server", 1), "asks the server what would be deleted, without deleting"))
	case "drain":
		return one(fnd(R3, c, "Evicts every workload from a node."))
	case "apply", "create", "scale", "patch", "replace", "rollout", "cordon", "uncordon", "taint", "label", "annotate", "edit":
		return one(fnd(R2, c, "Changes live cluster objects.").WithAlt(c.String()+" --dry-run=server", "previews the change without applying it"))
	}
	return one(fnd(R2, c, "Changes live cluster objects."))
}

func ruleTerraform(c shellparse.Command, f flags) []Finding {
	switch f.sub() {
	case "init":
		return one(fnd(R1, c, "Downloads providers and prepares the folder. Nothing real changes."))
	case "plan", "validate", "fmt", "show", "output", "version", "state", "providers", "graph":
		return one(fnd(R0, c, "Only reads or previews infrastructure."))
	case "destroy":
		return one(fnd(R3, c, "Destroys real infrastructure and everything running on it.").WithAlt(strings.Replace(c.String(), "destroy", "plan -destroy", 1), "shows what would be destroyed"))
	case "apply":
		if f.hasAny("-auto-approve") {
			return one(fnd(R3, c, "Changes real infrastructure without showing you the plan first.").WithAlt("terraform plan", "shows the plan first"))
		}
		return one(fnd(R2, c, "Changes real infrastructure after showing a plan."))
	}
	return one(fnd(R2, c, "Changes infrastructure state."))
}

var cloudDestroy = []string{"rb", "delete", "terminate", "destroy", "remove", "rm", "purge", "drop", "deregister", "detach", "release", "revoke"}

var cloudRead = []string{"describe", "list", "get", "ls", "show", "view", "info", "status", "whoami", "version"}

func ruleCloud(c shellparse.Command, f flags) []Finding {
	for _, p := range f.pos {
		low := strings.ToLower(p)
		for _, d := range cloudDestroy {
			if low == d || strings.HasPrefix(low, d+"-") {
				return one(fnd(R3, c, "Deletes cloud resources. This usually cannot be undone and can cost data or uptime."))
			}
		}
	}
	if f.hasAny("--delete") {
		return one(fnd(R3, c, "Deletes files at the destination that are missing from the source."))
	}
	for _, p := range f.pos {
		low := strings.ToLower(p)
		for _, d := range cloudRead {
			if low == d || strings.HasPrefix(low, d+"-") {
				return one(fnd(R0, c, "Only reads cloud information."))
			}
		}
	}
	return one(fnd(R2, c, "Changes cloud resources."))
}
