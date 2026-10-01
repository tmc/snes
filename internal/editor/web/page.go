package web

const page = `<!doctype html><meta charset="utf-8"><meta name="viewport" content="width=device-width"><title>SNES rotation editor</title>
<style>body{font:16px system-ui;background:#171c23;color:#e8edf2;margin:0}header,main{padding:24px;max-width:1100px;margin:auto}main{display:grid;grid-template-columns:1fr 1fr;gap:20px}section{background:#222b36;padding:20px;border-radius:8px}h1{margin:0}small{color:#adb9c6}button,select,input{font:inherit;padding:8px;background:#e8edf2;color:#17202a;border:0;border-radius:4px}pre{white-space:pre-wrap;overflow-wrap:anywhere}.tag{color:#9ad6c3}#error{color:#ffbdab}@media(max-width:700px){main{display:block}section{margin-bottom:16px}}</style>
<header><h1>Intro rotation</h1><p>Original observations · reversible draft · experiment evidence</p><small id="identity"></small><p id="error"></p></header>
<main><section><h2>Selected field</h2><select id="fields"></select><pre id="selection"></pre><h3>Observed</h3><p id="observed"></p><h3>Advisory interpretation</h3><p id="inferred"></p></section>
<section><h2>Parameter draft</h2><label>Initial angular phase <input id="draft" type="number"></label><p id="draftstate">No draft</p><button id="reset">Discard draft</button><p>Draft only. Changing this control does not execute C or alter the original capture.</p><h3>Original baseline</h3><p id="baseline"></p><h3>Experiment</h3><p>Not executed. No experimental result or proof eligibility.</p></section>
<section><h2>Frame</h2><p id="frame"></p><h3>Sprite explanation</h3><p id="sprite"></p></section><section><h2>Evidence pins</h2><pre id="pins"></pre><p>Hashes bind retained bytes; they do not establish admission, timing, or pixel ownership.</p></section></main>
<script>
fetch('/api/target').then(r=>{if(!r.ok)throw Error('Target unavailable');return r.json()}).then(m=>{
const t=m.target,o=m.observation,$=id=>document.getElementById(id);
$('identity').textContent=t.id+' · manifest '+m.manifest_sha256;
const fields=[t.parameter.field,...t.effects];fields.forEach((f,i)=>{let n=document.createElement('option');n.value=i;n.textContent=f.name;$('fields').append(n)});
function select(){let f=fields[Number($('fields').value)];$('selection').textContent='WRAM $'+f.address.toString(16).toUpperCase()+' · '+f.bytes+' byte(s)';}select();$('fields').onchange=select;
$('observed').textContent=o.handler_entries+' original intervals, frames '+o.observed_frames.join('–')+', '+o.contiguous_instructions_per_handler+' instructions and '+o.ordered_wram_writes_per_handler+' writes each. '+o.observed_scope;
$('inferred').textContent=t.advisory_crosswalk+' · Names and parameter meaning are advisory.';
const d=$('draft');d.min=t.parameter.minimum;d.max=t.parameter.maximum;
d.oninput=()=>{$('draftstate').textContent=d.value===''?'No draft':d.validity.valid?'Unexecuted draft: '+d.value:'Outside descriptor range';};$('reset').onclick=()=>{d.value='';d.oninput()};
$('baseline').textContent=m.baseline;$('frame').textContent=m.frame;$('sprite').textContent=m.sprite_provenance;$('pins').textContent=JSON.stringify(o.artifacts,null,2);
}).catch(e=>document.getElementById('error').textContent=e.message);
</script>`
