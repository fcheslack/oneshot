package oneshot

/* Current Implementation:
   SnapshotLocal or SnapshotRemote, indicates where to read the file from and how to treat relative paths inside the document
   read the local file or GET the remote file. at the moment, remote runs RunPhantom and uses files output by Phantomjs
   parse the content into a gokogiri DOM document
   call processDocument:
       fixupScripts: fetches and inlines any remote scripts
       remove script tags (alternative: neuter script tags)
       fixupCss: 
           expand CSS urls for styles that are already inline
           fetch remote CSS, change from link to style elements, expand urls to absolute, and inline the content
       findImageReferences (populate keys in our imageData map for later fetching of values):
           src attribute from img elements
           values inside url() in style elements
       fetch the files referenced by each key in the imageData map, datafy it, and save it as the value in imageData map
       go back through document and replace img src and css url() references with the data urls
   write out the document to a file
*/

/*
 - Allow removing of javascript to prevent double execution
 - capture DOM state after initial javascript execution so mutations are actually what user sees (phantomjs?)
 - inline CSS (not data, just need to put it into the tag)
 - remove embeds or dataurl them
 - CSS that gets inlined from remote file needs URLs to be based on remote file's url
 - CSS that is already inline needs URLs to be based on base page url
 - make sure to trim quotes from css urls
 -
 - download page html
 - Parse html into document
 - remove script tags
 - find all image tags, make src urls absolute, optionally datafy
 - find all style link tags
     make sure href is absolute
     if inliningCss: fetch css and make referred to urls absolute
 - second pass, find all style tags
     optionally datafy all referred to urls
 - handle embeds somehow
     absolutize or datafy src attribute (This does not appear to be supported by browsers for swf)
 - handle object tags somehow
     absolutize or datafy data attribute
 - handle more link tags than css?
*/
/*
 TODO: 
 tradeoff of where in the flow we put phantomjs (or whatever to run JS)
  - if we put phantom at the beginning of the process, all references to things we need to load will be present when we start looking, but we'll have to fetch resources twice, once by phantom and once when building the snapshot, since phantomjs doesn't give us access to the raw files.
  - if we put phantom at the end of the process, there is a chance it adds new references to resources we need to inline, so we need to go through the replacement process again.

 TODO: iframes - currently we do nothing with them. Even if content is inline, our dom methods should not reach them so they should be untouched.
   To deal with them without phantom, we can fetch the src, process as a separate document, then insert the content into the iframe and remove the src attr. This will not do any script evaluation, which seems problematic especially for iframes.
   If we're using Phantomjs, we can grab the frame content from inside there, output it as a separate document, and read/insert that, which will run scripts, but requires moving more logic to phantomjs.
 deal with images that were already base64 encoded
  - webpagedump saves these with ridiculous filenames, but the content seems fine.
    - the filename is missing the "data:image/" at the beggining, presumably thinking it was a path
    - filename is then first X chars of the rest of the dataurl (eg svg+xml;base64,PD94bWwgdmVyc2lvbj0iMS4wIiBlbmNvZGluZ...) with .dat extension
    - presumably the data in the files comes out fine because WPD just lets the browser resolve the url, which gets it a blob that works
    - for something like svg, this leads to many files with the same name, since the beginning of the file has the same xml content
Question: Does handling url() in CSS deal cleanly with @import? It looks like url() is not required and @import can simple be a quoted string, which would not work. May need to independently find those and change to data urls inside url().
*/

import (
	"encoding/base64"
	"fmt"
	"github.com/moovweb/gokogiri"
	"github.com/moovweb/gokogiri/html"
	"github.com/moovweb/gokogiri/xml"
	"io/ioutil"
	"log"
	"mime"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

type Snapshot struct {
	DocUrl               string
	DocLocation          string
	DocBaseUrl           string
	Doc                  *html.HtmlDocument
	Local                bool
	RemoveScripts        bool
	FetchRemoteCss       bool
	FetchRemoteImages    bool
	FetchRemoteCssImages bool
	//    makeRemoteAbsolute bool
	OutputFile string
	Wg         sync.WaitGroup
	ImageData  map[string]string
}

type DataUrl struct {
	Url  string
	Data string
}

func SnapshotLocal(snap *Snapshot) {
	body, err := ioutil.ReadFile(snap.DocUrl)
	if err != nil {
		log.Println("Error loading html document")
		os.Exit(1)
	}

	//parse html into document
	snap.Doc, err = gokogiri.ParseHtml(body)

	origout, _ := snap.Doc.ToHtml(nil, nil)
	ioutil.WriteFile(snap.OutputFile, origout, 0666)

	processDocument(snap)

	out, _ := snap.Doc.ToHtml(nil, nil)
	ioutil.WriteFile(snap.OutputFile, out, 0666)
	return
}

func SnapshotRemote(snap *Snapshot) {
	RunPhantom(snap.DocUrl, "./phantomdump")

	body, err := ioutil.ReadFile("./phantomdump.html")
	if err != nil {
		log.Println("Error loading html document")
		os.Exit(1)
	}
	/*
		resp, err := http.Get(snap.DocUrl)
		if err != nil {
			log.Println("Error loading html document")
			log.Println(err)
			os.Exit(1)
		}
		defer resp.Body.Close()
		body, _ := ioutil.ReadAll(resp.Body)
	*/
	//parse html into document
	snap.Doc, _ = gokogiri.ParseHtml(body)

	origout, _ := snap.Doc.ToHtml(nil, nil)
	ioutil.WriteFile(snap.OutputFile, origout, 0666)

	processDocument(snap)

	out, _ := snap.Doc.ToHtml(nil, nil)
	ioutil.WriteFile(snap.OutputFile, out, 0666)
	return
}

func processDocument(snap *Snapshot) {
	/*
	   if snap.RemoveScripts {
	       removeScriptTags(snap.Doc)
	   }
	*/
	fixupScripts(snap)
	//neuterScriptTags(snap.Doc)
	removeScriptTags(snap.Doc)
	//get all the nodes that may need to be modified
	//styleLinkNodes, _ := snap.Doc.Root().Search("//link[@rel='stylesheet' and @type='text/css']")

	/* first pass: mutate doc without requests to make urls absolute
	 * build map of images that need to be fetched and datafied
	 */

	fixupCss(snap)

	findImageReferences(snap)

	//echo images needing to be fetched
	/*

	   c := 0
	   for imgurl := range snap.ImageData {
	       log.Printf("%3d: %v\n", c, imgurl)
	       c++
	   }
	*/
	fetchImages(snap)

	/* second pass: mutate doc to add data urls
	 */
	imgNodes, _ := snap.Doc.Root().Search("//img")
	for i := range imgNodes {
		imgurl := imgNodes[i].Attr("src")
		dataurl, ok := snap.ImageData[imgurl]
		if ok {
			imgNodes[i].SetAttr("src", dataurl)
		} else {
			log.Printf("Error trying to set data url: url not found.\n")
		}
	}

	styleNodes, _ := snap.Doc.Root().Search("//style[@type='text/css']")
	for _, node := range styleNodes {
		content := node.Content()
		for imgurl, data := range snap.ImageData {
			content = strings.Replace(content, imgurl, data, -1)
		}
		node.SetContent(content)
	}
	/*
	   //get all binary references from embed nodes
	   embedNodes, _ := snap.Doc.Root().Search("//embed")
	   for i := range embedNodes {
	       embedurl := embedNodes[i].Attr("src")
	       dataurl, ok := snap.ImageData[embedurl]
	       if ok {
	           embedNodes[i].SetAttr("src", dataurl)
	       } else {
	           log.Printf("Error trying to set data url: url not found.\n")
	       }
	   }
	*/

	/* potential third pass: 
	   write out file
	   render with phantomjs so JS runs and potentially modifies DOM
	      (This could cause us to need to fetch+datafy+inline more)
	   save back file
	   make scripts inert so they don't execute a second time when viewing
	     the saved snapshot
	*/
}

func fetchImages(snap *Snapshot) {
	log.Println("FETCHING ALL IMAGES\n")
	//fetchChan := make(chan DataUrl)
	fetchedChan := make(chan DataUrl, 200)
	//fetch all images in parallel and datafy them
	var fetchFunc func(url string) string
	if snap.Local {
		fetchFunc = FetchAndDatafyLocal
	} else {
		fetchFunc = FetchAndDatafyRemote
	}

	for imgurl := range snap.ImageData {
		snap.Wg.Add(1)
		go func(imgurl string, fetchedChan chan DataUrl) {
			fetchedChan <- DataUrl{imgurl, fetchFunc(imgurl)}
			snap.Wg.Done()
		}(imgurl, fetchedChan)
	}
	//log.Println("DONE LAUNCHING GOROUTINES\n")
	snap.Wg.Wait()
	close(fetchedChan)
	//log.Println("CHAN CLOSED")

	for u := range fetchedChan {
		//log.Printf("Adding data to snap.ImageData for %s\n", u.Url)
		snap.ImageData[u.Url] = u.Data
	}

	log.Println("DONE FETCHING ALL IMAGES\n")

}

func removeScriptTags(doc *html.HtmlDocument) {
	//remove script tags
	scriptNodes, _ := doc.Root().Search("//script")
	for s := range scriptNodes {
		scriptNodes[s].Remove()
	}
}

//alter script tags without removing them such that they do not execute but their
//original content/functionality can be retrieved
func neuterScriptTags(doc *html.HtmlDocument) {
	//change name of script tags so they don't execute but we can restore them
	scriptNodes, _ := doc.Root().Search("//script")
	for s := range scriptNodes {
		node := scriptNodes[s]
		PreserveAttr(node, "type")
		node.SetAttr("type", "text/noexecutejs")
	}
}

//alter css links and inline css such that styles and references are preserved
//when put in an unrelated context and the original location or resources
//are no longer available
func fixupCss(snap *Snapshot) {
	//expand CSS Urls for already inline styles
	styleNodes, _ := snap.Doc.Root().Search("//style[@type='text/css']")
	for i := range styleNodes {
		styleContent := styleNodes[i].Content()
		expandedStyleContent := ExpandCssUrls(styleContent, snap.DocUrl)
		styleNodes[i].SetContent(expandedStyleContent)
	}

	styleLinkNodes, _ := snap.Doc.Root().Search("//link[@rel='stylesheet' and @type='text/css']")

	//inline CSS first if we're going to do it at all
	if snap.FetchRemoteCss {
		for i := range styleLinkNodes {
			//log.Printf("Fetching remote css\n")
			snap.Wg.Add(1)
			go func(xmlNode xml.Node, burl string) {
				InlineCss(xmlNode, burl)
				snap.Wg.Done()
			}(styleLinkNodes[i], snap.DocUrl)
		}
	} else {
		//just make urls absolute
		for i := range styleLinkNodes {
			MakeAttrAbsolute(styleLinkNodes[i], snap.DocUrl, "href")
		}
	}

	//wait on IO requests
	snap.Wg.Wait()
	return
}

func InlineCss(xmlNode xml.Node, baseUrl string) {
	absoluteCssUrl := AbsoluteUrl(xmlNode.Attr("href"), baseUrl)

	body := Fetch(absoluteCssUrl)
	if body == nil {
		//failed to retrieve remove CSS
		xmlNode.SetAttr("href", absoluteCssUrl)
		xmlNode.SetAttr("data-failedload", absoluteCssUrl)
	} else {
		//log.Printf("Non-nil Body returned to InlineCss\n")
		xmlNode.SetName("style")
		xmlNode.SetAttr("type", "text/css")
		xmlNode.Attribute("rel").Remove()
		xmlNode.Attribute("href").Remove()

		//expand css urls to be absolute, target is based on location of CSS file
		urlExpandedBody := ExpandCssUrls(string(body), absoluteCssUrl)
		xmlNode.SetContent(urlExpandedBody)
	}

	//log.Printf("Absolute Css Url:%s\n", absoluteCssUrl)
}

func ExpandCssUrls(cssBody string, baseUrl string) string {
	urlMatcher := regexp.MustCompile(`url\((.*?)\)`)
	matches := urlMatcher.FindAllStringSubmatch(cssBody, -1)
	for i := range matches {
		//ignore url arguments that are already data uri
		if !strings.HasPrefix(matches[i][1], "data") {
			aurl := AbsoluteUrl(strings.Trim(matches[i][1], " '\""), baseUrl)
			log.Printf("URL:%v\nAbsURL:%v\n\n", matches[i][1], aurl)
			newUrl := fmt.Sprintf("url(%s)", aurl)
			cssBody = strings.Replace(cssBody, matches[i][0], newUrl, 1)
		}
	}
	return cssBody
}

func fixupScripts(snap *Snapshot) {
	scriptNodes, _ := snap.Doc.Root().Search("//script")

	//inline scripts
	for i := range scriptNodes {
		//log.Printf("Fetching remote css\n")
		snap.Wg.Add(1)
		go func(xmlNode xml.Node, burl string) {
			InlineScript(xmlNode, burl)
			snap.Wg.Done()
		}(scriptNodes[i], snap.DocUrl)
	}
	//wait on IO requests
	snap.Wg.Wait()
	return
}

func InlineScript(xmlNode xml.Node, baseUrl string) {
	if nil == xmlNode.Attribute("src") {
		//bail out early since script is already inline
		return
	}
	absoluteScriptUrl := AbsoluteUrl(xmlNode.Attr("src"), baseUrl)

	body := Fetch(absoluteScriptUrl)
	if body == nil {
		//failed to retrieve remove script
		xmlNode.SetAttr("src", absoluteScriptUrl)
		xmlNode.SetAttr("data-oneshot-failedload", absoluteScriptUrl)
	} else {
		//log.Printf("Non-nil Body returned to InlineScript\n")
		PreserveAttr(xmlNode, "src")
		xmlNode.Attribute("src").Remove()
		xmlNode.SetContent(body)
	}
}

func findImageReferences(snap *Snapshot) {
	imgNodes, _ := snap.Doc.Root().Search("//img")

	//get all image references from img nodes
	for i := range imgNodes {
		if !strings.HasPrefix(imgNodes[i].Attr("src"), "data") {
			snap.ImageData[MakeAttrAbsolute(imgNodes[i], snap.DocUrl, "src")] = "" //mutate element and add empty entry to imageMap
		}
	}
	//get all image references from style nodes
	styleNodes, _ := snap.Doc.Root().Search("//style[@type='text/css']")
	for _, node := range styleNodes {
		urlMatcher := regexp.MustCompile(`url\((.*?)\)`)
		matches := urlMatcher.FindAllStringSubmatch(node.Content(), -1)
		for i := range matches {
			if !strings.HasPrefix(matches[i][1], "data") {
				snap.ImageData[matches[i][1]] = "" //pull out the already absolute sub match
			}
		}
	}

	/*
	   //get all binary references from embed nodes
	   embedNodes, _ := snap.Doc.Root().Search("//embed")
	   for i := range embedNodes {
	       if !strings.HasPrefix(embedNodes[i].Attr("src"), "data"){
	           snap.ImageData[MakeAttrAbsolute(embedNodes[i], snap.DocUrl, "src")] = "" //mutate element and add empty entry to imageMap
	       }
	   }
	*/
}

func AbsoluteUrl(urlString string, baseUrl string) string {
	base, _ := url.Parse(baseUrl)
	relUrl, _ := url.Parse(urlString)
	absUrl := base.ResolveReference(relUrl)
	return absUrl.String()
}

func MakeAttrAbsolute(xmlNode xml.Node, baseUrl string, attrName string) (absurl string) {
	PreserveAttr(xmlNode, attrName)

	absurl = AbsoluteUrl(xmlNode.Attr(attrName), baseUrl)
	xmlNode.SetAttr(attrName, absurl)
	return
}

func PreserveAttr(xmlNode xml.Node, attrName string) {
	//check that the Attr even exists here first
	/*if nil == xmlNode.Attribute(attrName) {
		return
	}*/

	//preserve original value in an extra data attr
	origVal := xmlNode.Attr(attrName)
	if "" == xmlNode.Attr("data-oneshot-orig-"+attrName) {
		xmlNode.SetAttr("data-oneshot-orig-"+attrName, origVal)
	}
}

func Fetch(url string) []byte {
	//log.Printf("Fetching file %s\n", url)
	var body []byte
	var err error
	if url[0] == "/"[0] {
		//log.Printf("Reading local file %s\n", url)
		body, err = ioutil.ReadFile(url)
		if err != nil {
			log.Printf("Error reading file %s\n", url)
			return nil
		}
	} else {
		//log.Printf("Reading remote file %s\n", url)
		resp, err := http.Get(url)
		if err != nil {
			return nil
		}
		if resp.StatusCode != 200 {
			return nil
		}
		defer resp.Body.Close()
		//mime := resp.Header["Content-Type"]
		body, _ = ioutil.ReadAll(resp.Body)
	}
	//log.Printf("Done Fetching file %s\n", url)
	return body
}

func FetchAndDatafyLocal(url string) string {
	//log.Printf("Fetch and datafying file %s\n", url)
	var body []byte
	var err error
	var mimeType string
	body, err = ioutil.ReadFile(url)
	if err != nil {
		log.Printf("Error Fetch and datafying local file %s\n", url)
		return ""
	}

	mimeType = mime.TypeByExtension(filepath.Ext(url))
	//log.Printf("mimetype of local file %s with extension %s is %v", url, filepath.Ext(url), mimeType)
	//data:[<MIME-type>][;charset=<encoding>][;base64],<data>
	encoded := make([]byte, base64.StdEncoding.EncodedLen(len(body)))
	base64.StdEncoding.Encode(encoded, body)

	if mimeType == "" {
		log.Printf("MIME IS NIL FOR %s\n", url)
		return "data:" + ";base64," + string(encoded)
	}
	//log.Printf("returning encoded file %s\n", url)
	return "data:" + mimeType + ";base64," + string(encoded)
}

func FetchAndDatafyRemote(url string) string {
	//log.Printf("Fetch and datafying file %s\n", url)
	var body []byte
	var err error
	var mimeType []string
	resp, err := http.Get(url)
	if err != nil {
		log.Printf("Error Fetch and datafying remote file %s\n", url)
		return ""
	}
	if resp.StatusCode != 200 {
		log.Printf("Error Fetch and datafying remote file %s\n", url)
		return ""
	}
	defer resp.Body.Close()
	mimeType = resp.Header["Content-Type"]
	body, _ = ioutil.ReadAll(resp.Body)

	//data:[<MIME-type>][;charset=<encoding>][;base64],<data>
	encoded := make([]byte, base64.StdEncoding.EncodedLen(len(body)))
	base64.StdEncoding.Encode(encoded, body)

	if mimeType == nil {
		log.Printf("MIME IS NIL FOR %s\n", url)
		return "data:" + ";base64," + string(encoded)
	}
	//log.Printf("returning encoded file %s\n", url)
	return "data:" + mimeType[0] + ";base64," + string(encoded)
}

//load a local html file with phantomjs, allow it to execute javascript and
//save back out to another file
func RunPhantom(infilepath string, outfilepath string) {
	cmd := exec.Command("phantomjs", "phantomdump.js", infilepath, outfilepath)
	err := cmd.Start()
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("Waiting for command to finish...")
	err = cmd.Wait()
	log.Printf("Command finished with error: %v", err)

}
