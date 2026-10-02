package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/tmc/snes/internal/editor/statewrites"
)

// StateWritesHandler serves observed byte writes from a frozen timeline.
// The caller is responsible for binding the listener to loopback.
func StateWritesHandler(timeline *statewrites.Timeline) http.Handler {
	mux := http.NewServeMux()
	registerStateWrites(mux, timeline)
	return mux
}

func registerStateWrites(mux *http.ServeMux, timeline *statewrites.Timeline) {
	// Copy the full snapshot before serving, including optional values and slices.
	var frozen *statewrites.Timeline
	if timeline != nil {
		raw, err := json.Marshal(timeline)
		if err == nil {
			var copy statewrites.Timeline
			if json.Unmarshal(raw, &copy) == nil {
				frozen = &copy
			}
		}
	}
	get := func(w http.ResponseWriter, r *http.Request) bool {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			http.Error(w, "read-only endpoint", http.StatusMethodNotAllowed)
			return false
		}
		return true
	}
	mux.HandleFunc("/statewrites", func(w http.ResponseWriter, r *http.Request) {
		if !get(w, r) {
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; connect-src 'self'")
		_, _ = w.Write([]byte(stateWritesPage))
	})
	mux.HandleFunc("/api/statewrites", func(w http.ResponseWriter, r *http.Request) {
		if !get(w, r) {
			return
		}
		if frozen == nil {
			http.Error(w, "state-write evidence unavailable", 404)
			return
		}
		if len(r.URL.RawQuery) > 512 {
			http.Error(w, "query too long", 400)
			return
		}
		q, err := url.ParseQuery(r.URL.RawQuery)
		if err != nil {
			http.Error(w, "invalid query encoding", 400)
			return
		}
		for key, values := range q {
			if (key != "address" && key != "from" && key != "to") || len(values) != 1 {
				err = fmt.Errorf("unknown or repeated query parameter")
				break
			}
		}
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		if len(q) == 0 {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"schema": frozen.Schema, "window_sha256": frozen.WindowSHA256, "identity": frozen.Identity, "from": frozen.From, "to": frozen.To, "limitations": frozen.Limitations})
			return
		}
		address, err := parseStateAddress(q.Get("address"))
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		from, e1 := strconv.Atoi(q.Get("from"))
		to, e2 := strconv.Atoi(q.Get("to"))
		if e1 != nil || e2 != nil || from < frozen.From || to > frozen.To || from >= to {
			http.Error(w, "frame interval must be within the captured half-open interval", 400)
			return
		}
		writes, err := frozen.Select(address, from, to)
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		if len(writes) > 20000 {
			http.Error(w, "more than 20000 writes; select a smaller frame interval", 422)
			return
		}
		rows := make([]map[string]any, 0, len(writes))
		for _, v := range writes {
			var context any
			if v.Context != nil {
				context = map[string]any{"entry": v.Context.Entry, "ordinal": strconv.FormatUint(v.Context.Ordinal, 10)}
			}
			rows = append(rows, map[string]any{"frame": v.Frame, "ppu_frame": v.PPUFrame, "ordinal": strconv.FormatUint(v.Ordinal, 10), "cycle": strconv.FormatUint(v.Cycle, 10), "actor": v.Actor, "kind": v.Kind, "writer_pc": v.WriterPC, "raw_address": v.RawAddress, "address": v.Address, "before": v.Before, "after": v.After, "context": context})
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"schema": frozen.Schema, "window_sha256": frozen.WindowSHA256, "from": from, "to": to, "writes": rows, "count": len(rows), "limitations": frozen.Limitations})
	})
}

func parseStateAddress(s string) (uint32, error) {
	s = strings.TrimPrefix(strings.TrimPrefix(strings.TrimPrefix(s, "$"), "0x"), "0X")
	if len(s) == 7 && s[2] == ':' {
		s = s[:2] + s[3:]
	}
	if len(s) != 6 {
		return 0, fmt.Errorf("address must be six hexadecimal digits")
	}
	v, err := strconv.ParseUint(s, 16, 24)
	if err != nil {
		return 0, fmt.Errorf("invalid hexadecimal address")
	}
	return uint32(v), nil
}

const stateWritesPage = `<!doctype html><html lang="en"><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>SNES state writes</title>
<style>:root{color-scheme:dark}body{background:#171c23;color:#e8edf2;font:16px system-ui;margin:0}main{max-width:1180px;margin:auto;padding:32px}a{color:#9ad6c3}input,button{font:inherit;padding:8px;margin:4px;background:#e8edf2;color:#171c23;border:0;border-radius:4px}section{background:#222b36;padding:18px;margin-top:20px;border-radius:8px}table{border-collapse:collapse;width:100%}td,th{text-align:left;vertical-align:top;padding:10px;border-bottom:1px solid #445262}small{color:#adb9c6}#identity{overflow-wrap:anywhere}.scroll{overflow:auto}#status{white-space:pre-wrap}label{display:inline-block}button:disabled{opacity:.5}</style>
<main><a href="/">Home</a><h1>Observed state writes</h1><p>Follow one storage byte through a captured frame interval. Repeated writes remain separate events.</p><small id="identity"></small>
<section><form id="query"><label>WRAM address <input id="address" placeholder="$7E:0010" maxlength="9" required aria-label="WRAM address"></label><label>From frame <input id="from" type="number" min="0" required></label><label>To frame (excluded) <input id="to" type="number" min="1" required></label><button id="select" disabled>Show writes</button></form><p id="status" role="status">Loading capture identity…</p><div class="scroll"><table><thead><tr><th>Frame / PPU frame</th><th>Event / cycle</th><th>Writer</th><th>Recorded address → storage</th><th>Before → after</th><th>Dispatch context</th></tr></thead><tbody id="rows"></tbody></table></div></section>
<section><h2>Evidence and limits</h2><p>This is a recorded trace, not a live machine or a game variable label. No changed value implies pixel ownership.</p><ul id="limits"></ul></section></main>
<script>const $=id=>document.getElementById(id);const hex=(v,n)=>'$'+v.toString(16).toUpperCase().padStart(n,'0');function node(tag,value){const n=document.createElement(tag);n.textContent=value;return n}let request=0;
async function response(url){const r=await fetch(url);if(!r.ok)throw Error(await r.text());return r.json()}
response('/api/statewrites').then(m=>{$('identity').textContent='Captured host frames ['+m.from+', '+m.to+') · window SHA-256 '+m.window_sha256;$('from').value=m.from;$('to').value=m.to;$('select').disabled=false;$('status').textContent='Select an observed WRAM byte. No address is inferred from a game label.';for(const x of m.limitations)$('limits').append(node('li',x))}).catch(e=>{$('status').textContent='Unavailable: '+e.message});
$('query').onsubmit=async e=>{e.preventDefault();const id=++request;$('rows').replaceChildren();$('status').textContent='Reading frozen evidence…';try{const params=new URLSearchParams({address:$('address').value,from:$('from').value,to:$('to').value});const m=await response('/api/statewrites?'+params);if(id!==request)return;for(const v of m.writes){const row=document.createElement('tr');row.append(node('td',v.frame+' / '+v.ppu_frame),node('td',v.ordinal+' / '+v.cycle));const writer=node('td',v.actor+' · ');if(v.writer_pc===null)writer.append(node('span','writer PC unknown'));else{writer.append(node('code',hex(v.writer_pc,6)),node('small',' · code navigation unavailable'))}row.append(writer,node('td',(v.kind==='wram_port'?'WRAM port offset ':'CPU bus ')+hex(v.raw_address,6)+' → '+hex(v.address,6)),node('td',(v.before===null?'unknown':hex(v.before,2))+' → '+hex(v.after,2)),node('td',v.context===null?'unknown (not captured)':JSON.stringify(v.context)));$('rows').append(row)}$('status').textContent=m.count===0?'No observed writes in this captured interval. This does not establish that the byte never changes.':m.count+' observed writes in ['+m.from+', '+m.to+'). Before values are observed history, not inferred initialization.';}catch(e){if(id===request)$('status').textContent='Selection unavailable: '+e.message}};</script></html>`
