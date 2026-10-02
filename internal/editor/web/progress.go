package web

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/tmc/snes/internal/recovery/progress"
)

// ProgressHandler serves an immutable read-only progress snapshot and evidence.
// The caller is responsible for binding the listener to loopback.
func ProgressHandler(report *progress.Report) http.Handler {
	mux := http.NewServeMux()
	registerProgress(mux, report)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		http.Redirect(w, r, "/progress", http.StatusSeeOther)
	})
	return mux
}

func registerProgress(mux *http.ServeMux, report *progress.Report) {
	body, _ := json.Marshal(report)
	var evidence [][]byte
	if report != nil {
		for i := range report.Sources {
			b, _ := report.Evidence(i)
			evidence = append(evidence, b)
		}
	}
	get := func(w http.ResponseWriter, r *http.Request) bool {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			http.Error(w, "read-only endpoint", 405)
			return false
		}
		return true
	}
	mux.HandleFunc("/progress", func(w http.ResponseWriter, r *http.Request) {
		if !get(w, r) {
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; connect-src 'self'")
		w.Write([]byte(progressPage))
	})
	mux.HandleFunc("/api/progress", func(w http.ResponseWriter, r *http.Request) {
		if !get(w, r) {
			return
		}
		if report == nil {
			http.Error(w, "progress evidence unavailable", 404)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(body)
	})
	mux.HandleFunc("/api/progress/evidence", func(w http.ResponseWriter, r *http.Request) {
		if !get(w, r) {
			return
		}
		i, e := strconv.Atoi(r.URL.Query().Get("index"))
		if e != nil || i < 0 || i >= len(evidence) {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(evidence[i])
	})
}

const progressPage = `<!doctype html><html lang="en"><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>SNES recovery progress</title>
<style>:root{color-scheme:dark}body{margin:0;background:#171c23;color:#e8edf2;font:16px system-ui}main{max-width:1180px;margin:auto;padding:32px}a{color:#9ad6c3}h1{margin-bottom:8px}small,.muted{color:#adb9c6;overflow-wrap:anywhere}#cards{display:grid;grid-template-columns:repeat(5,1fr);gap:12px;margin:24px 0}.card,section{background:#222b36;padding:18px;border-radius:8px}.number{font-size:30px;font-weight:650;margin:8px 0}.unit{min-height:40px}section{margin:20px 0}table{width:100%;border-collapse:collapse}td,th{text-align:left;padding:10px;border-bottom:1px solid #445262;vertical-align:top}th{font-size:13px;color:#adb9c6}input,select,button{font:inherit;padding:8px;background:#e8edf2;color:#17202a;border:0;border-radius:4px}pre{white-space:pre-wrap;overflow-wrap:anywhere}.bank{display:grid;grid-template-columns:140px 1fr 180px;gap:12px;margin:12px 0;align-items:center}progress{width:100%;accent-color:#9ad6c3}#error{color:#ffbdab}.scroll{overflow:auto}summary{cursor:pointer}@media(max-width:750px){#cards{grid-template-columns:1fr 1fr}.bank{grid-template-columns:1fr}main{padding:16px}}</style>
<main><a href="/">Home</a><h1>Recovery progress</h1><p>What ran, what was decoded, and what survived comparison.</p><small id="identity"></small><p id="error" role="status">Loading recorded evidence…</p><div id="cards"></div>
<section><h2>ROM map</h2><p>Physical 32 KiB segments. Bars show decoded bytes; reached starts are a separate count. Undecoded bytes may be code, data or padding.</p><details><summary>Inspect physical ROM segments</summary><div id="banks"></div></details></section>
<section><h2>Selected candidates and blockers</h2><p id="batch"></p><label>Filter <input id="search" type="search" placeholder="Address, candidate, stage or reason"></label> <select id="status"><option value="">All outcomes</option><option value="accepted">Accepted</option><option value="refused">Refused</option><option value="unexecuted">Unexecuted</option></select><p id="rowcount"></p><div class="scroll"><table><thead><tr><th>Entry / candidate</th><th>Workflow checkpoint</th><th>Captured / emitted / qualified</th><th>Blocker and next step</th></tr></thead><tbody id="rows"></tbody></table></div></section>
<section><h2>Evidence and scope</h2><p>No single “percent done”: these measures use different units and scopes.</p><ul id="limits"></ul><div id="sources"></div><details><summary>Snapshot identities</summary><pre id="pins"></pre></details></section></main>
<script>const $=id=>document.getElementById(id);function text(tag,value){let n=document.createElement(tag);n.textContent=value;return n}
fetch('/api/progress').then(async r=>{if(!r.ok)throw Error(await r.text());return r.json()}).then(m=>{
$('error').textContent='';$('identity').textContent='ROM '+m.rom_sha256+' · '+m.rom_bytes.toLocaleString()+' bytes · recorded snapshot';
for(const v of m.metrics){let c=text('div','');c.className='card';c.append(text('strong',v.name));let n=text('div',v.count===null?'Unavailable':v.count);n.className='number';c.append(n);let u=text('div',v.unit);u.className='unit';c.append(u,text('small',v.scope));$('cards').append(c)}
for(const b of m.banks){let row=text('div','');row.className='bank';row.append(text('span','ROM $'+b.offset.toString(16).toUpperCase().padStart(6,'0')));let bar=document.createElement('progress');bar.max=b.bytes;bar.value=b.decoded_bytes;bar.setAttribute('aria-label',b.decoded_bytes+' decoded bytes of '+b.bytes);row.append(bar,text('small',b.decoded_bytes+' decoded bytes · '+(b.reached_starts===null?'reached unavailable':b.reached_starts+' reached starts')));$('banks').append(row)}
$('batch').textContent=m.batch_status+(m.batch_runtime_sha256?' · recorded execution '+m.batch_runtime_sha256:'')+'. Entries shown here are selected batch candidates, not all game routines.';
function rows(){let q=$('search').value.toLowerCase(),s=$('status').value;if(/^\$?(?:0x)?[0-9a-f]{2}:?[0-9a-f]{4}$/.test(q))q=q.replace(/^\$/,'').replace(/^0x/,'').replace(':','');let entries=m.candidates.filter(c=>(!s||c.status===s)&&[c.id,c.entry.toString(16),c.stage,c.reason,c.status].join(' ').toLowerCase().includes(q));$('rows').replaceChildren();for(const c of entries){let row=document.createElement('tr');let entry=text('td','$'+c.entry.toString(16).toUpperCase().padStart(6,'0'));entry.append(text('div',c.id));row.append(entry,text('td',c.stage+' · '+c.status),text('td',(c.captured===null?'capture unavailable':c.captured+' complete')+' / '+(c.emitted?'C artifact':'no verified C artifact')+' / '+c.qualified+' sampled matches'));let why=text('td',c.reason||'No blocker recorded');why.append(text('p',c.action));row.append(why);$('rows').append(row)}$('rowcount').textContent=entries.length+' of '+m.candidates.length+' selected entries'+(m.candidates.length?'':' · candidate evidence unavailable');}rows();$('search').oninput=rows;$('status').onchange=rows;
for(const x of m.limitations)$('limits').append(text('li',x));m.sources.forEach((x,i)=>{let p=text('p','');let a=text('a',x.name);a.href='/api/progress/evidence?index='+i;a.target='_blank';a.rel='noopener';p.append(a,text('small',' · SHA-256 '+x.sha256));$('sources').append(p)});$('pins').textContent=JSON.stringify({schema:m.schema,config:m.config_sha256,rom:m.rom_sha256,recorded_batch_runtime:m.batch_runtime_sha256,sources:m.sources},null,2);
}).catch(e=>{$('error').textContent='Progress unavailable: '+e.message});</script></html>`
