package main

import (
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/i2y/ramune"
)

// JSON: generate 200 objects, filter, map, serialize (~0.1ms per request)
const helloHandler = `
	globalThis.__poolHandle = function(req) {
		var data = [];
		for (var i = 0; i < 200; i++) {
			data.push({ id: i, val: Math.sqrt(i * 31337), name: "item_" + i });
		}
		var out = data.filter(function(d) { return d.val > 50; })
			.map(function(d) { return { id: d.id, v: Math.round(d.val*100)/100 }; });
		return { status: 200, body: JSON.stringify(out) };
	};
`

func main() {
	maxWorkers := 3
	if len(os.Args) > 1 {
		if n, err := strconv.Atoi(os.Args[1]); err == nil && n > 0 {
			maxWorkers = n
		}
	}

	if len(os.Args) > 2 && os.Args[2] == "single" {
		n := maxWorkers
		fmt.Printf("--- %d Worker(s) ---\n", n)
		runBench(n)
		return
	}

	fmt.Printf("=== Pool HTTP Benchmark (GOMAXPROCS=%d) ===\n", runtime.GOMAXPROCS(0))
	fmt.Println("Handler: JSON generate/filter/map (200 objects)")
	fmt.Println()
	for n := 1; n <= maxWorkers; n++ {
		cmd := exec.Command(os.Args[0], strconv.Itoa(n), "single")
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		cmd.Run()
		fmt.Println()
	}
}

func runBench(n int) {
	pool, err := ramune.NewPool(n)
	if err != nil {
		fmt.Fprintf(os.Stderr, "pool(%d): %v\n", n, err)
		os.Exit(1)
	}
	defer func() {
		pool.StopHTTP()
		pool.Close()
	}()

	go func() {
		pool.ListenAndServe(":0", helloHandler)
	}()

	var addr string
	for i := 0; i < 100; i++ {
		time.Sleep(10 * time.Millisecond)
		addr = pool.Addr()
		if addr != "" {
			break
		}
	}
	if addr == "" {
		fmt.Fprintln(os.Stderr, "server did not start")
		os.Exit(1)
	}

	// Warm up
	for i := 0; i < 100; i++ {
		http.Get("http://" + addr + "/")
	}

	url := "http://" + addr + "/"
	out, _ := exec.Command("wrk", "-t4", "-c100", "-d10s", url).CombinedOutput()
	for _, l := range strings.Split(string(out), "\n") {
		l = strings.TrimSpace(l)
		if l != "" {
			fmt.Println("  " + l)
		}
	}
}
