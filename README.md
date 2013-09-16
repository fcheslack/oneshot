oneshot
=======

Tool to take a snapshot of a web page and dump it into a single html file, taking advantage of data urls to keep even binary data (mostly images) in the document and require no other files. This way it can be viewed in any modern browser without special plugins, is easy to ship around and for other toosl to deal with (relatively) without taking special file naming/directory layouts into account, and prevents you from having to keep potentially hundreds of extra files around for a single page.

This is still just a first pass implementation and still has a number of problems. The saving strategy and concerns were influenced by the paper describing webpagedump http://www.dbai.tuwien.ac.at/user/pollak/webpagedump/ .

Current Implementation:
- SnapshotLocal or SnapshotRemote, indicates where to read the file from and how to treat relative paths inside the document
- read the local file or GET the remote file. at the moment, remote runs RunPhantom and uses files output by Phantomjs
- parse the content into a gokogiri DOM document
- call processDocument:
    - fixupScripts: fetches and inlines any remote scripts
    - remove script tags (alternative: neuter script tags)
    - fixupCss: 
        - expand CSS urls for styles that are already inline
        - fetch remote CSS, change from link to style elements, expand urls to absolute, and inline the content
    - findImageReferences (populate keys in our imageData map for later fetching of values):
        - src attribute from img elements
        - values inside url() in style elements
    - fetch the files referenced by each key in the imageData map, datafy it, and save it as the value in imageData map
    - go back through document and replace img src and css url() references with the data urls
- write out the document to a file