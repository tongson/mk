package main

import (
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"sync"
)

// True if messages should be printed without fancy colors.
var nocolor bool = false

// True if we are no actualyl executing any recipes or updating any timestamps.
var dryrun bool = false

// True if we are ignoring timestamps and rebuilding everything.
var rebuildall bool = false

// Lock on standard out, messages don't get interleaved too much.
var mkMsgMutex sync.Mutex

// The maximum number of times an rule may be applied.
const maxRuleCnt = 1

// Limit the number of recipes executed simultaneously.
var subprocsAllowed int
var subprocsAllowedCond *sync.Cond = sync.NewCond(&sync.Mutex{})

// Wait until there is an available subprocess slot.
func reserveSubproc() {
	subprocsAllowedCond.L.Lock()
	for subprocsAllowed == 0 {
		subprocsAllowedCond.Wait()
	}
	subprocsAllowed--
	subprocsAllowedCond.L.Unlock()
}

// Free up another subprocess to run.
func finishSubproc() {
	subprocsAllowedCond.L.Lock()
	subprocsAllowed++
	subprocsAllowedCond.Signal()
	subprocsAllowedCond.L.Unlock()
}

// Ansi color codes.
const (
	ansiTermDefault   = "\033[0m"
	ansiTermBlack     = "\033[30m"
	ansiTermRed       = "\033[31m"
	ansiTermGreen     = "\033[32m"
	ansiTermYellow    = "\033[33m"
	ansiTermBlue      = "\033[34m"
	ansiTermMagenta   = "\033[35m"
	ansiTermBright    = "\033[1m"
	ansiTermUnderline = "\033[4m"
)

func mk(rs *ruleSet, target string, dryrun bool) {
	g := buildgraph(rs, target)
	if g.root.exists && !rebuildall {
		return
	}
    mkNode(g, g.root)
}

// Build a target in the graph.
//
// This selects an appropriate rule (edge) and builds all prerequisites
// concurrently.
//
func mkNode(g *graph, u *node) {
	// try to claim on this node
	u.mutex.Lock()
	if u.status != nodeStatusReady {
		u.mutex.Unlock()
		return
	} else {
		u.status = nodeStatusStarted
	}
	u.mutex.Unlock()

	// when finished, notify the listeners
	finalstatus := nodeStatusDone
	defer func() {
		u.mutex.Lock()
		u.status = finalstatus
		u.mutex.Unlock()
		for i := range u.listeners {
			u.listeners[i] <- u.status
		}
	}()

	// there's no fucking rules, dude
	if len(u.prereqs) == 0 {
		if !(u.r != nil && u.r.attributes.virtual) && !u.exists {
			wd, _ := os.Getwd()
			mkError(fmt.Sprintf("don't know how to make %s in %s", u.name, wd))
		}
		return
	}

	// there should otherwise be exactly one edge with an associated rule
	prereqs := make([]*node, 0)
	var e *edge = nil
	for i := range u.prereqs {
		if u.prereqs[i].r != nil {
			e = u.prereqs[i]
		}
		if u.prereqs[i].v != nil {
			prereqs = append(prereqs, u.prereqs[i].v)
		}
	}

	// this should have been caught during graph building
	if e == nil {
		wd, _ := os.Getwd()
		mkError(fmt.Sprintf("don't know how to make %s in %s", u.name, wd))
	}

	prereqstat := make(chan nodeStatus)
	pending := 0

	// build prereqs that need building
	e.r.mutex.Lock()
	for i := range prereqs {
		prereqs[i].mutex.Lock()
		// needs to be built?
        u.updateTimestamp()
		if !prereqs[i].exists || e.r.attributes.virtual || rebuildall || (u.exists && u.t.Before(prereqs[i].t)) {
			switch prereqs[i].status {
			case nodeStatusReady:
				go mkNode(g, prereqs[i])
				fallthrough
			case nodeStatusStarted:
				prereqs[i].listeners = append(prereqs[i].listeners, prereqstat)
				pending++
			}
		}
		prereqs[i].mutex.Unlock()
	}
	e.r.mutex.Unlock()

	// wait until all the prereqs are built
	for pending > 0 {
		s := <-prereqstat
		pending--
		if s == nodeStatusFailed {
			finalstatus = nodeStatusFailed
		}
	}

	// execute the recipe, unless the prereqs failed
	if finalstatus != nodeStatusFailed && len(e.r.recipe) > 0 {
		reserveSubproc()
		if !dorecipe(u.name, u, e) {
			finalstatus = nodeStatusFailed
		}
		finishSubproc()
	}
}

func mkError(msg string) {
	mkPrintError(msg)
	os.Exit(1)
}

func mkPrintError(msg string) {
	if !nocolor {
		os.Stderr.WriteString(ansiTermRed)
	}
	fmt.Fprintf(os.Stderr, "%s", msg)
	if !nocolor {
		os.Stderr.WriteString(ansiTermDefault)
	}
}

func mkPrintSuccess(msg string) {
	if nocolor {
		fmt.Println(msg)
	} else {
		fmt.Printf("%s%s%s\n", ansiTermGreen, msg, ansiTermDefault)
	}
}

func mkPrintMessage(msg string) {
	mkMsgMutex.Lock()
	if nocolor {
		fmt.Println(msg)
	} else {
		fmt.Printf("%s%s%s\n", ansiTermBlue, msg, ansiTermDefault)
	}
	mkMsgMutex.Unlock()
}

func mkPrintRecipe(target string, recipe string) {
	mkMsgMutex.Lock()
	if nocolor {
		fmt.Printf("%s: ", target)
	} else {
		fmt.Printf("%s%s%s → %s",
			ansiTermBlue+ansiTermBright+ansiTermUnderline, target,
			ansiTermDefault, ansiTermBlue)
	}
	printIndented(os.Stdout, recipe, len(target)+3)
	if len(recipe) == 0 {
		os.Stdout.WriteString("\n")
	}
	if !nocolor {
		os.Stdout.WriteString(ansiTermDefault)
	}
	mkMsgMutex.Unlock()
}

func main() {
	var mkfilepath string
	flag.StringVar(&mkfilepath, "f", "mkfile", "use the given file as mkfile")
	flag.BoolVar(&dryrun, "n", false, "print commands without actually executing")
	flag.BoolVar(&rebuildall, "a", false, "force building of all dependencies")
	flag.IntVar(&subprocsAllowed, "p", 8, "maximum number of jobs to execute in parallel")
	flag.Parse()

	mkfile, err := os.Open(mkfilepath)
	if err != nil {
		mkError("no mkfile found")
	}
	input, _ := ioutil.ReadAll(mkfile)
	mkfile.Close()

	rs := parse(string(input), mkfilepath)
	targets := flag.Args()

	// build the first non-meta rule in the makefile, if none are given explicitly
    if len(targets) == 0 {
        for i := range rs.rules {
            if !rs.rules[i].ismeta {
                for j := range rs.rules[i].targets {
                    targets = append(targets, rs.rules[i].targets[j].spat)
                }
                break
            }
        }
    }

	if len(targets) == 0 {
		fmt.Println("mk: nothing to mk")
		return
	}

	// TODO: For multiple targets, we should add a dummy rule that depends on
	// all let mk handle executing each.
    for _, target := range targets {
        mk(rs, target, dryrun)
    }
}
