package cli

import (
	"strconv"
	"strings"
)

// parseQueryFlags 手动解析 query 参数（flags 与位置参数任意顺序）。
func parseQueryFlags(args []string) queryFlags {
	f := queryFlags{repoPath: "."}
	f.queryMaxHops, f.writeMaxHops, f.readMaxHops = -1, -1, -1
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--repo" && i+1 < len(args):
			f.repoPath = ResolveRepoRef(args[i+1])
			i++
		case strings.HasPrefix(a, "--repo="):
			f.repoPath = ResolveRepoRef(strings.TrimPrefix(a, "--repo="))
		case a == "--include-untyped":
			f.includeUntyped = true
		case a == "--depth" && i+1 < len(args):
			f.depth, _ = strconv.Atoi(args[i+1])
			i++
		case strings.HasPrefix(a, "--depth="):
			f.depth, _ = strconv.Atoi(strings.TrimPrefix(a, "--depth="))
		case a == "--max-depth" && i+1 < len(args):
			f.maxDepth, _ = strconv.Atoi(args[i+1])
			i++
		case strings.HasPrefix(a, "--max-depth="):
			f.maxDepth, _ = strconv.Atoi(strings.TrimPrefix(a, "--max-depth="))
		case a == "--func" && i+1 < len(args):
			f.funcPath = args[i+1]
			i++
		case strings.HasPrefix(a, "--func="):
			f.funcPath = strings.TrimPrefix(a, "--func=")
		case a == "--since" && i+1 < len(args):
			f.since = args[i+1]
			i++
		case strings.HasPrefix(a, "--since="):
			f.since = strings.TrimPrefix(a, "--since=")
		case a == "--fail-on" && i+1 < len(args):
			f.failOn = args[i+1]
			i++
		case strings.HasPrefix(a, "--fail-on="):
			f.failOn = strings.TrimPrefix(a, "--fail-on=")
		case a == "--min-conf" && i+1 < len(args):
			f.minConf, _ = strconv.ParseFloat(args[i+1], 64)
			f.minConfSet = true
			i++
		case strings.HasPrefix(a, "--min-conf="):
			f.minConf, _ = strconv.ParseFloat(strings.TrimPrefix(a, "--min-conf="), 64)
			f.minConfSet = true
		case a == "--min-packages" && i+1 < len(args):
			f.minPkgs, _ = strconv.Atoi(args[i+1])
			i++
		case strings.HasPrefix(a, "--min-packages="):
			f.minPkgs, _ = strconv.Atoi(strings.TrimPrefix(a, "--min-packages="))
		case a == "--include-container":
			f.includeContainer = true
		case a == "--follow-indirect":
			f.followIndirect = true
		case a == "--all":
			f.all = true
		case a == "--type" && i+1 < len(args):
			f.relTypes = append(f.relTypes, strings.Split(args[i+1], ",")...)
			i++
		case strings.HasPrefix(a, "--type="):
			f.relTypes = append(f.relTypes, strings.Split(strings.TrimPrefix(a, "--type="), ",")...)
		case a == "--max-hops" && i+1 < len(args):
			f.maxHops, _ = strconv.Atoi(args[i+1])
			i++
		case strings.HasPrefix(a, "--max-hops="):
			f.maxHops, _ = strconv.Atoi(strings.TrimPrefix(a, "--max-hops="))
		case a == "--max-results" && i+1 < len(args):
			f.maxResults, _ = strconv.Atoi(args[i+1])
			i++
		case strings.HasPrefix(a, "--max-results="):
			f.maxResults, _ = strconv.Atoi(strings.TrimPrefix(a, "--max-results="))
		case a == "--include-long-query":
			f.includeLongQuery = true
		case a == "--query-max-hops" && i+1 < len(args):
			f.queryMaxHops, _ = strconv.Atoi(args[i+1])
			i++
		case strings.HasPrefix(a, "--query-max-hops="):
			f.queryMaxHops, _ = strconv.Atoi(strings.TrimPrefix(a, "--query-max-hops="))
		case a == "--write-max-hops" && i+1 < len(args):
			f.writeMaxHops, _ = strconv.Atoi(args[i+1])
			i++
		case strings.HasPrefix(a, "--write-max-hops="):
			f.writeMaxHops, _ = strconv.Atoi(strings.TrimPrefix(a, "--write-max-hops="))
		case a == "--read-max-hops" && i+1 < len(args):
			f.readMaxHops, _ = strconv.Atoi(args[i+1])
			i++
		case strings.HasPrefix(a, "--read-max-hops="):
			f.readMaxHops, _ = strconv.Atoi(strings.TrimPrefix(a, "--read-max-hops="))
		case a == "--memory" && i+1 < len(args):
			f.memory = args[i+1]
			i++
		case strings.HasPrefix(a, "--memory="):
			f.memory = strings.TrimPrefix(a, "--memory=")
		case a == "--yaml" && i+1 < len(args):
			f.yamlPath = args[i+1]
			i++
		case strings.HasPrefix(a, "--yaml="):
			f.yamlPath = strings.TrimPrefix(a, "--yaml=")
		case a == "--max-entries" && i+1 < len(args):
			f.maxEntries, _ = strconv.Atoi(args[i+1])
			i++
		case strings.HasPrefix(a, "--max-entries="):
			f.maxEntries, _ = strconv.Atoi(strings.TrimPrefix(a, "--max-entries="))
		case a == "--code":
			f.code = true
		case a == "--json":
			f.json = true
		case a == "--full":
			f.full = true
		case a == "--compact":
			f.compact = true
		case a == "--format" && i+1 < len(args):
			f.format = args[i+1]
			i++
		case strings.HasPrefix(a, "--format="):
			f.format = strings.TrimPrefix(a, "--format=")
		case strings.HasPrefix(a, "-"):

		default:
			f.positional = append(f.positional, a)
		}
	}
	return f
}
