package main

import (
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	_ "net/http/pprof"

	"github.com/c-sto/recursebuster/librecursebuster"
	"github.com/fatih/color"
)

const version = "1.1.0"

func main() {
	if runtime.GOOS == "windows" { //lol goos
		//can't use color.Error, because *nix etc don't have that for some reason :(
		librecursebuster.InitLogger(color.Output, color.Output, color.Output, color.Output, color.Output, color.Output, color.Output, color.Output, color.Output, color.Output)
	} else {
		librecursebuster.InitLogger(os.Stdout, os.Stdout, os.Stdout, os.Stdout, os.Stdout, os.Stdout, os.Stdout, os.Stdout, os.Stdout, os.Stderr)
	}

	wg := &sync.WaitGroup{}
	cfg := librecursebuster.Config{}

	//the state should probably change per different host.. eventually
	globalState := librecursebuster.State{
		BadResponses: make(map[int]bool),
		Whitelist:    make(map[string]bool),
		Blacklist:    make(map[string]bool),
	}
	globalState.Hosts.Init()

	cfg.Version = version
	totesTested := uint64(0)
	globalState.TotalTested = &totesTested
	showVersion := true
	flag.BoolVar(&cfg.ShowAll, "all", false, "Show, and write the result of all checks")
	flag.BoolVar(&cfg.AppendDir, "appendslash", false, "Append a / to all directory bruteforce requests (like extension, but slash instead of .yourthing)")
	flag.StringVar(&cfg.Auth, "auth", "", "Basic auth. Supply this with the base64 encoded portion to be placed after the word 'Basic' in the Authorization header.")
	flag.StringVar(&cfg.BadResponses, "bad", "404", "Responses to consider 'bad' or 'not found'. Comma-separated This works the opposite way of gobuster!")
	flag.StringVar(&cfg.BlacklistLocation, "blacklist", "", "Blacklist of prefixes to not check. Will not check on exact matches.")
	flag.StringVar(&cfg.Canary, "canary", "", "Custom value to use to check for wildcards")
	flag.BoolVar(&cfg.CleanOutput, "clean", false, "Output clean URLs to the output file for easy loading into other tools and whatnot.")
	flag.StringVar(&cfg.Cookies, "cookies", "", "Any cookies to include with requests. This is smashed into the cookies header, so copy straight from burp I guess.")
	flag.BoolVar(&cfg.Debug, "debug", false, "Enable debugging")
	flag.IntVar(&cfg.MaxDirs, "dirs", 1, "Maximum directories to perform busting on concurrently NOTE: directories will still be brute forced, this setting simply directs how many should be concurrently bruteforced")
	flag.StringVar(&cfg.Extensions, "ext", "", "Extensions to append to checks. Multiple extensions can be specified, comma separate them.")
	flag.Var(&cfg.Headers, "headers", "Additional headers to include with request. Supply as key:value. Can specify multiple - eg '-headers X-Forwarded-For:127.0.01 -headers X-ATT-DeviceId:XXXXX'")
	flag.BoolVar(&cfg.HTTPS, "https", false, "Use HTTPS instead of HTTP.")
	flag.StringVar(&cfg.InputList, "iL", "", "File to use as an input list of URL's to start from")
	flag.BoolVar(&cfg.SSLIgnore, "k", false, "Ignore SSL check")
	flag.BoolVar(&cfg.ShowLen, "len", false, "Show, and write the length of the response")
	flag.BoolVar(&cfg.NoGet, "noget", false, "Do not perform a GET request (only use HEAD request/response)")
	flag.BoolVar(&cfg.NoRecursion, "norecursion", false, "Disable recursion, just work on the specified directory. Also disables spider function.")
	flag.BoolVar(&cfg.NoSpider, "nospider", false, "Don't search the page body for links, and directories to add to the spider queue.")
	flag.BoolVar(&cfg.NoStatus, "nostatus", false, "Don't print status info (for if it messes with the terminal)")
	flag.StringVar(&cfg.Localpath, "o", "."+string(os.PathSeparator)+"busted.txt", "Local file to dump into")
	flag.StringVar(&cfg.ProxyAddr, "proxy", "", "Proxy configuration options in the form ip:port eg: 127.0.0.1:9050. Note! If you want this to work with burp/use it with a HTTP proxy, specify as http://ip:port")
	flag.Float64Var(&cfg.Ratio404, "ratio", 0.95, "Similarity ratio to the 404 canary page.")
	flag.BoolVar(&cfg.FollowRedirects, "redirect", false, "Follow redirects")
	flag.BoolVar(&cfg.BurpMode, "sitemap", false, "Send 'good' requests to the configured proxy. Requires the proxy flag to be set. ***NOTE: with this option, the proxy is ONLY used for good requests - all other requests go out as normal!***")
	flag.IntVar(&cfg.Threads, "t", 1, "Number of concurrent threads")
	flag.IntVar(&cfg.Timeout, "timeout", 20, "Timeout (seconds) for HTTP/TCP connections")
	flag.StringVar(&cfg.URL, "u", "", "Url to spider")
	flag.StringVar(&cfg.Agent, "ua", "RecurseBuster/"+version, "User agent to use when sending requests.")
	flag.IntVar(&cfg.VerboseLevel, "v", 0, "Verbosity level for output messages.")
	flag.BoolVar(&showVersion, "version", false, "Show version number and exit")
	flag.StringVar(&cfg.Wordlist, "w", "", "Wordlist to use for bruteforce. Blank for spider only")
	flag.StringVar(&cfg.WhitelistLocation, "whitelist", "", "Whitelist of domains to include in brute-force")

	flag.Parse()

	if cfg.Debug {
		go func() {
			http.ListenAndServe("localhost:6061", http.DefaultServeMux)
		}()
	}

	if showVersion {
		librecursebuster.PrintBanner(cfg)
		os.Exit(0)
	}

	printChan := make(chan librecursebuster.OutLine, 200)
	if cfg.URL == "" && cfg.InputList == "" {
		flag.Usage()
		os.Exit(1)
	}

	var h *url.URL
	var err error
	URLSlice := []string{} //
	if cfg.URL != "" {
		URLSlice = append(URLSlice, cfg.URL)
	}
	if cfg.InputList != "" { //can have both -u flag and -iL flag
		//must be using an input list
		URLList := make(chan string, 10)
		go librecursebuster.LoadWords(cfg.InputList, URLList, printChan)
		for x := range URLList {
			//ensure all urls will parse good
			_, err = url.Parse(x)
			if err != nil {
				panic("URL parse fail: " + err.Error())
			}
			URLSlice = append(URLSlice, x)
			//globalState.Whitelist[u.Host] = true
		}
	}

	h, err = url.Parse(URLSlice[0])

	if err != nil {
		panic("URL parse fail")
	}

	if h.Scheme == "" {
		if cfg.HTTPS {
			h, err = url.Parse("https://" + URLSlice[0])
		} else {
			h, err = url.Parse("http://" + URLSlice[0])
		}
	}

	for _, x := range strings.Split(cfg.Extensions, ",") {
		globalState.Extensions = append(globalState.Extensions, x)
	}

	for _, x := range strings.Split(cfg.BadResponses, ",") {
		i, err := strconv.Atoi(x)
		if err != nil {
			panic(err)
		}
		globalState.BadResponses[i] = true //this is probably a candidate for individual urls. Unsure how to config that cleanly though
	}

	globalState.Hosts.AddHost(h)
	//state.ParsedURL = h
	client := librecursebuster.ConfigureHTTPClient(cfg, wg, printChan, false)

	//setup channels
	pages := make(chan librecursebuster.SpiderPage, 1000)
	newPages := make(chan librecursebuster.SpiderPage, 10000)
	confirmed := make(chan librecursebuster.SpiderPage, 1000)
	workers := make(chan struct{}, cfg.Threads)
	maxDirs := make(chan struct{}, cfg.MaxDirs)
	testChan := make(chan string, 100)

	globalState.Client = client

	if cfg.BlacklistLocation != "" {
		readerChan := make(chan string, 100)
		go librecursebuster.LoadWords(cfg.BlacklistLocation, readerChan, printChan)
		for x := range readerChan {
			globalState.Blacklist[x] = true
		}
	}

	if cfg.WhitelistLocation != "" {
		readerChan := make(chan string, 100)
		go librecursebuster.LoadWords(cfg.WhitelistLocation, readerChan, printChan)
		for x := range readerChan {
			globalState.Whitelist[x] = true
		}
	}

	if cfg.Wordlist != "" && cfg.MaxDirs == 1 {

		zerod := uint32(0)
		globalState.DirbProgress = &zerod

		zero := uint32(0)
		globalState.WordlistLen = &zero

		readerChan := make(chan string, 100)
		go librecursebuster.LoadWords(cfg.Wordlist, readerChan, printChan)
		for _ = range readerChan {
			atomic.AddUint32(globalState.WordlistLen, 1)
		}
	}

	canary := librecursebuster.RandString(printChan)

	if cfg.Canary != "" {
		canary = cfg.Canary
	}

	librecursebuster.PrintBanner(cfg)

	//do first load of urls (send canary requests to make sure we can dirbust them)

	globalState.StartTime = time.Now()
	globalState.PerSecondShort = new(uint64)
	globalState.PerSecondLong = new(uint64)

	go librecursebuster.StatusPrinter(cfg, globalState, wg, printChan, testChan)
	go librecursebuster.ManageRequests(cfg, globalState, wg, pages, newPages, confirmed, workers, printChan, maxDirs, testChan)
	go librecursebuster.ManageNewURLs(cfg, globalState, wg, pages, newPages, printChan)
	go librecursebuster.OutputWriter(wg, cfg, confirmed, cfg.Localpath, printChan)
	go librecursebuster.StatsTracker(globalState)

	librecursebuster.PrintOutput("Starting recursebuster...     ", librecursebuster.Info, 0, wg, printChan)

	//seed the workers
	for _, s := range URLSlice {
		u, err := url.Parse(s)
		if err != nil {
			panic(err)
		}

		if u.Scheme == "" {
			if cfg.HTTPS {
				u, err = url.Parse("https://" + s)
			} else {
				u, err = url.Parse("http://" + s)
			}
		}

		//do canary etc
		prefix := u.String()
		if len(prefix) > 0 && string(prefix[len(prefix)-1]) != "/" {
			prefix = prefix + "/"
		}
		randURL := fmt.Sprintf("%s%s", prefix, canary)
		resp, content, err := librecursebuster.HttpReq("GET", randURL, client, cfg)
		if err != nil {
			panic("Canary Error, check url is correct: " + randURL + "\n" + err.Error())
		}
		librecursebuster.PrintOutput(
			fmt.Sprintf("Canary sent: %s, Response: %v", randURL, resp.Status),
			librecursebuster.Debug, 2, wg, printChan,
		)

		globalState.Hosts.AddSoft404Content(u.Host, content) // Soft404ResponseBody = xx

		x := librecursebuster.SpiderPage{}
		x.URL = u.String()
		x.Reference = u

		if !strings.HasSuffix(u.String(), "/") {
			wg.Add(1)
			pages <- librecursebuster.SpiderPage{
				URL:       h.String() + "/",
				Reference: h,
			}
		}

		wg.Add(1)
		pages <- x
	}

	//wait for completion
	wg.Wait()

}
