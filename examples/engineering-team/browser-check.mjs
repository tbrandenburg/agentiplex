// Optional real Chromium smoke test using Node 22's built-in WebSocket/CDP.
import { spawn } from "node:child_process";
import { mkdtemp, readFile, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { setTimeout as delay } from "node:timers/promises";

const origin = process.argv[2];
if (!origin || !/^http:\/\/127\.0\.0\.1:[0-9]+$/.test(origin)) throw new Error("Expected a local demo URL");
const profile = await mkdtemp(join(tmpdir(),"agentiplex-browser-"));
const flags = ["--headless","--disable-dev-shm-usage","--remote-debugging-port=0","--user-data-dir="+profile,"about:blank"];
if (process.env.CI === "true") flags.unshift("--no-sandbox");
const chrome = spawn(process.env.CHROME_BIN || "google-chrome",flags,{stdio:"ignore"});
let spawnError, ws;
chrome.on("error",error=>{spawnError=error;});
const exited = new Promise(resolve=>chrome.once("close",resolve));
async function eventually(fn,timeout=20000){
  const end=Date.now()+timeout;
  let last;
  while(Date.now()<end){
    if(spawnError)throw spawnError;
    try{const value=await fn();if(value)return value;}catch(e){last=e;}
    await delay(250);
  }
  throw new Error("Browser assertion timed out: "+(last?.message || "condition not met"));
}
try{
  const port=await eventually(async()=> (await readFile(join(profile,"DevToolsActivePort"),"utf8")).split("\n")[0]);
  const pages=await (await fetch("http://127.0.0.1:"+port+"/json/list")).json();
  const page=pages.find(p=>p.type==="page");
  if(!page)throw new Error("No Chromium page");
  ws=new WebSocket(page.webSocketDebuggerUrl);
  await new Promise((resolve,reject)=>{
    const timer=setTimeout(()=>reject(new Error("CDP connection timeout")),5000);
    ws.addEventListener("open",()=>{clearTimeout(timer);resolve();},{once:true});
    ws.addEventListener("error",()=>{clearTimeout(timer);reject(new Error("CDP connection failed"));},{once:true});
  });
  let next=0;
  const calls=new Map();
  ws.addEventListener("message",e=>{
    const m=JSON.parse(e.data);
    const cb=calls.get(m.id);
    if(cb){calls.delete(m.id);cb(m);}
  });
  function cdp(method,params={}){
    const id=++next;
    return new Promise((resolve,reject)=>{
      const timer=setTimeout(()=>{calls.delete(id);reject(new Error(method+" timed out"));},5000);
      calls.set(id,m=>{clearTimeout(timer);if(m.error)reject(new Error(m.error.message));else resolve(m.result);});
      ws.send(JSON.stringify({id,method,params}));
    });
  }
  await cdp("Page.navigate",{url:origin+"/ui/"});
  await eventually(async()=>{
    const r=await cdp("Runtime.evaluate",{expression:
      'document.querySelector(".amis-scope") !== null && document.body.innerText.includes("Registered agents") && document.body.innerText.includes("tester-2") && document.body.innerText.includes("Simulated fixture") && document.body.innerText.includes("Recent deliveries") && document.body.innerText.includes("Recent events") && [...document.querySelectorAll("button,a")].filter(e=>e.textContent.trim()==="Inspect").length===7',
      returnByValue:true});
    return r.result.value===true;
  });
  await cdp("Runtime.evaluate",{expression:'[...document.querySelectorAll("button,a")].find(e=>e.textContent.trim()==="Inspect")?.click()'});
  await eventually(async()=>{
    const r=await cdp("Runtime.evaluate",{expression:'document.body.innerText.includes("Delivery detail") && document.body.innerText.includes("SIMULATED") && document.body.innerText.includes("completed")',returnByValue:true});
    return r.result.value===true;
  });
  // Exercise a real post-connect mutation and confirm it arrives in the DOM.
  const update=await fetch(origin+"/api/v1/groups/browser-check",{
    method:"PUT",headers:{"Content-Type":"application/json"},body:JSON.stringify({members:["pm"]})
  });
  if(!update.ok)throw new Error("Live group update failed: "+update.status);
  await eventually(async()=>{
    const r=await cdp("Runtime.evaluate",{expression:'document.body.innerText.includes("browser-check") && document.body.innerText.includes("2 / 1")',returnByValue:true});
    return r.result.value===true;
  });
  console.log("PASS: AMIS rendered the team, seven delivery actions, stored detail and automatic group/event refresh.");
}finally{
  ws?.close();
  if(chrome.pid)chrome.kill();
  await Promise.race([exited,delay(3000)]);
  if(chrome.exitCode===null&&chrome.signalCode===null&&chrome.pid)chrome.kill("SIGKILL");
  await rm(profile,{recursive:true,force:true,maxRetries:3,retryDelay:200});
}
