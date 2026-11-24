"use strict";
const $ = id => document.getElementById(id);
const node = (tag, text, cls) => {
  const n = document.createElement(tag);
  if (text !== undefined) n.textContent = text;
  if (cls) n.className = cls;
  return n;
};
const states = {completed:"good",queued:"active",accepted:"active",attempting:"active",preparing:"active",partial:"warning","input-required":"warning","auth-required":"warning",failed:"bad",uncertain:"bad"};
const pill = state => node("span",state || "—","pill " + (states[state] || ""));
const fmtTime = value => value ? new Date(value).toLocaleTimeString() : "—";
const json = async path => {
  const response = await fetch(path,{cache:"no-store",signal:AbortSignal.timeout(12000)});
  if (!response.ok) throw new Error("HTTP " + response.status + " " + path);
  return response.json();
};
const cell = (row,value) => {const td=node("td");td.append(value instanceof Node ? value : document.createTextNode(String(value ?? "—")));row.append(td);};
let events=[],cursor=0,refreshTimer,refreshing=false,refreshAgain=false,selected="",detailVersion=0;
function renderAgents(agents){
  $("agent-count").textContent=agents.length;
  const root=$("agents");root.replaceChildren();
  if(!agents.length)root.append(node("p","No registered agents.","note"));
  for(const a of agents){
    const el=node("article",undefined,"agent");
    const skills=a.card?.skills || [];
    const tags=skills.flatMap(s=>s.tags || []);
    const simulated=tags.includes("simulated");
    el.append(node("strong",a.id),node("span",simulated?"Simulated fixture":"External agent","pill "+(simulated?"warning":"active")),node("small",skills.map(s=>s.name || s.id).join(", ") || "No advertised skills"),node("small",a.endpoint),node("small","Registered · reachability not monitored"));
    root.append(el);
  }
}
function renderCollections(groups,pools){
  $("collection-count").textContent=groups.length+" / "+pools.length;
  const root=$("collections");root.replaceChildren();
  for(const c of [...groups,...pools]){
    const el=node("div",undefined,"collection");
    el.append(node("strong",c.id),node("span",c.kind==="pool"?"ONE OF MANY":"ONE TO MANY","pill"),node("small",c.members.join(" · ")));root.append(el);
  }
  if(!groups.length&&!pools.length)root.append(node("p","No configured collections.","note"));
}
function renderDeliveries(deliveries){
  const pending=new Set(["preparing","queued","attempting","accepted"]);
  $("pending-count").textContent=deliveries.filter(d=>pending.has(d.state)).length;
  $("completed-count").textContent=deliveries.filter(d=>d.state==="completed").length;
  $("attention-count").textContent=deliveries.filter(d=>!pending.has(d.state)&&d.state!=="completed").length;
  const root=$("deliveries");root.replaceChildren();
  for(const d of deliveries){
    const tr=node("tr");cell(tr,d.kind);cell(tr,d.target+(d.assignedAgent?" → "+d.assignedAgent:""));cell(tr,pill(d.state));
    cell(tr,d.children?.length ? d.children.filter(c=>c.state==="completed").length+"/"+d.children.length : "—");
    cell(tr,fmtTime(d.updatedAt));const b=node("button","Inspect");b.type="button";b.addEventListener("click",()=>showDetail(d.id));cell(tr,b);root.append(tr);
  }
  if(!deliveries.length){const tr=node("tr");const td=node("td","No deliveries yet.");td.colSpan=6;tr.append(td);root.append(tr);}
}
async function showDetail(id){
  selected=id;const version=++detailVersion;
  $("detail-panel").hidden=false;$("detail").textContent="Loading "+id;
  try{
    const d=await json("/api/v1/deliveries/"+encodeURIComponent(id));
    if(version!==detailVersion)return;
    $("detail").textContent=JSON.stringify(d,null,2);
    const root=$("children");root.replaceChildren();
    for(const c of d.children || []){
      const b=node("button",c.target+" · "+c.state);b.type="button";b.addEventListener("click",()=>showDetail(c.id));root.append(b);
    }
  }catch(e){if(version===detailVersion)$("detail").textContent=e.message;}
}
$("close-detail").addEventListener("click",()=>{selected="";detailVersion++;$("detail-panel").hidden=true;});
async function refresh(){
  if(refreshing){refreshAgain=true;return;}
  refreshing=true;
  try{
    const [agents,groups,pools,deliveries,ready]=await Promise.all([json("/api/v1/agents"),json("/api/v1/groups"),json("/api/v1/pools"),json("/api/v1/deliveries?limit=50"),fetch("/readyz",{signal:AbortSignal.timeout(5000)})]);
    renderAgents(agents);renderCollections(groups,pools);renderDeliveries(deliveries);
    $("broker").textContent=ready.ok?"Daemon ready":"Not ready";$("broker").className="pill "+(ready.ok?"good":"bad");
    $("updated").textContent="Snapshot "+new Date().toLocaleTimeString();$("error").hidden=true;
    if(selected)void showDetail(selected);
  }catch(e){$("error").textContent="Snapshot unavailable: "+e.message;$("error").hidden=false;$("broker").textContent="Readiness unknown";$("broker").className="pill warning";}
  finally{refreshing=false;if(refreshAgain){refreshAgain=false;scheduleRefresh();}}
}
function scheduleRefresh(){if(refreshTimer)return;refreshTimer=setTimeout(()=>{refreshTimer=null;void refresh();},400);}
function renderEvents(){
  const root=$("events");root.replaceChildren();
  for(const e of events.slice().reverse()){
    const tr=node("tr");cell(tr,e.sequence);cell(tr,fmtTime(e.at));cell(tr,e.kind);cell(tr,e.id.length>28?e.id.slice(0,25)+"…":e.id);cell(tr,pill(e.state));root.append(tr);
  }
}
const source=new EventSource("/api/v1/events/stream");
source.onopen=()=>{$("connection").textContent="Live events";$("connection").className="pill good";scheduleRefresh();};
source.onmessage=event=>{
  try{
    const e=JSON.parse(event.data);if(e.sequence<=cursor)return;cursor=e.sequence;
    events.push(e);if(events.length>80)events.shift();renderEvents();scheduleRefresh();
  }catch(error){$("error").textContent="Invalid event: "+error.message;$("error").hidden=false;}
};
source.onerror=()=>{$("connection").textContent="Reconnecting";$("connection").className="pill warning";scheduleRefresh();};
void refresh();setInterval(scheduleRefresh,5000);
