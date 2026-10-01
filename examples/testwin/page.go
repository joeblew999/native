//go:build darwin || windows

package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sync"
	"time"
)

// Swatch is the solid region the capture tests look for, in CSS pixels from
// the top-left of the web content.
const page = `<!doctype html>
<html><head><style>
html,body{margin:0;height:100%;background:#ffffff;font:14px -apple-system,sans-serif;overflow:hidden}
#swatch{position:absolute;left:0;top:0;width:200px;height:100px;background:#ff00ff}
#field{position:absolute;left:10px;top:120px;width:360px;height:24px}
#btn{position:absolute;left:10px;top:160px;width:160px;height:60px}
#scroll{position:absolute;left:200px;top:160px;width:180px;height:120px;overflow:scroll;background:#eeeeee}
</style></head><body>
<div id="swatch"></div>
<input id="field" autofocus>
<button id="btn">clicks: 0</button>
<div id="scroll"><div style="height:2000px;width:2000px"></div></div>
<script>
(function(){
  var field = document.getElementById('field');
  var btn = document.getElementById('btn');
  var clicks = 0;
  function mods(e){
    var m = [];
    if (e.shiftKey) m.push('shift');
    if (e.ctrlKey) m.push('control');
    if (e.altKey) m.push('option');
    if (e.metaKey) m.push('command');
    return m;
  }
  document.addEventListener('keydown', function(e){
    report({type:'key', key:e.key, code:e.code, mods:mods(e)});
  }, true);
  var swatch = document.getElementById('swatch');
  field.addEventListener('input', function(){
    // The swatch turns cyan while the field holds text, so a capture can see
    // that background typing changed the page.
    swatch.style.background = (field.value || clicks) ? '#00ffff' : '#ff00ff';
    report({type:'input', value:field.value});
  });
  document.addEventListener('mousedown', function(e){
    report({type:'mousedown', x:e.clientX, y:e.clientY, button:e.button});
  }, true);
  document.addEventListener('mouseup', function(e){
    report({type:'mouseup', x:e.clientX, y:e.clientY, button:e.button});
  }, true);
  document.addEventListener('click', function(e){
    if (e.target === btn) { clicks++; btn.textContent = 'clicks: ' + clicks; swatch.style.background = '#00ffff'; }
    report({type:'click', x:e.clientX, y:e.clientY, button:e.button, count:clicks});
  }, true);
  document.addEventListener('contextmenu', function(e){
    e.preventDefault();
    report({type:'click', x:e.clientX, y:e.clientY, button:e.button, count:clicks});
  }, true);
  document.addEventListener('wheel', function(e){
    report({type:'wheel', dx:e.deltaX, dy:e.deltaY});
  }, {capture:true, passive:true});
  // Visibility and focus as the engine sees them: WebView2 hides a page whose
  // window is fully covered, and the key tests need to know.
  document.addEventListener('visibilitychange', function(){
    report({type:'visibility', state:document.visibilityState});
  });
  window.addEventListener('focus', function(){ report({type:'focus', focused:true}); });
  window.addEventListener('blur', function(){ report({type:'focus', focused:false}); });
  field.focus();
  // Not requestAnimationFrame: WebKit stops it for an occluded window, and
  // this one sits behind everything. The capture test waits for the swatch.
  setTimeout(function(){ report({type:'ready', visibility:document.visibilityState, focused:document.hasFocus()}); }, 100);
})();
</script></body></html>`

var out = struct {
	sync.Mutex
	enc *json.Encoder
}{enc: json.NewEncoder(os.Stdout)}

func emit(v any) {
	out.Lock()
	defer out.Unlock()
	_ = out.enc.Encode(v)
}

// exitWithParent ends the process when stdin closes (the test that started it
// is gone) or after life, whichever comes first.
func exitWithParent(life time.Duration) {
	go func() {
		_, _ = io.Copy(io.Discard, bufio.NewReader(os.Stdin))
		os.Exit(0)
	}()
	go func() {
		time.Sleep(life)
		os.Exit(0)
	}()
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "testwin:", err)
	os.Exit(1)
}
