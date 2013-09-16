package main

import (
	"flag"
	"fmt"
	"github.com/fcheslack/oneshot"
	//	"log"
	"net/url"

//	"os/exec"
)

func main() {
	//parse command line flags and set defaults
	hurl := flag.String("url", "http://www.nytimes.com", "URL to snapshot")
	outputFile := flag.String("o", "./snapout", "Output file")
	localFiles := flag.Bool("localFiles", false, "Load a local html file and treat all urls as local file references")
	removeScripts := flag.Bool("removeScripts", false, "Remove script tags so they don't run")
	fetchRemoteImages := flag.Bool("fetchRemoteImages", false, "Fetch remote files and add data uris")
	fetchRemoteCss := flag.Bool("fetchRemoteCss", true, "Fetch remote CSS files and inline as style tags")
	fetchRemoteCssImages := flag.Bool("fetchRemoteCssImages", false, "Fetch remote images referred to in CSS files")
	//phantom := flag.Bool("phantom", true, "Use Phantomjs to preprocess page")
	//    makeRemoteAbsolute = flag.Bool("makeRemoteAbsolute", true, "Make urls for remote files absolute")
	flag.Parse()

	snap := new(oneshot.Snapshot)

	snap = &oneshot.Snapshot{
		DocUrl:               *hurl,
		DocLocation:          *hurl,
		DocBaseUrl:           *hurl,
		Local:                *localFiles,
		RemoveScripts:        *removeScripts,
		FetchRemoteCss:       *fetchRemoteCss,
		FetchRemoteImages:    *fetchRemoteImages,
		FetchRemoteCssImages: *fetchRemoteCssImages,
		OutputFile:           *outputFile}

	snap.ImageData = make(map[string]string)

	parsedUrl, _ := url.Parse(snap.DocUrl)
	if parsedUrl.Scheme == "" {
		snap.Local = true
	}

	//if using phantom: first pass with snap to inline everything without stripping out script tags
	//run through phantom to process javascript once, then take phantom's output and make script tags inoperable

	fmt.Printf("%v\n", snap)

	if snap.Local {
		oneshot.SnapshotLocal(snap)
	} else {
		oneshot.SnapshotRemote(snap)
	}
}
