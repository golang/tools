// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// httpGET requests a URL for its effects only.
// (It is needed for /open URLs; see objHTML.)
function httpGET(url) {
	var x = new XMLHttpRequest();
	x.open("GET", url, true);
	x.send();
	return false; // disable usual <a href=...> behavior
}

// disconnect banner
window.addEventListener('load', function() {
	// Create a hidden <div id='disconnected'> element.
	var banner = document.createElement("div");
	banner.id = "disconnected";
	banner.innerText = "Gopls server has terminated. Page is inactive.";
	document.body.appendChild(banner);

	// Start a GET /hang request. If it ever completes, the server
	// has disconnected. Reveal the banner in that case.
	var x = new XMLHttpRequest();
	x.open("GET", "/hang", true);
	x.onloadend = () => { banner.style.display = "block"; };
	x.send();
});
