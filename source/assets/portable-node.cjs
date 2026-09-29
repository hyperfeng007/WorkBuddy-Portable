'use strict';
// Synchronous: the daemon must install this BEFORE any original module executes.
const tls = require('node:tls');
const fs = require('node:fs');
const path = require('node:path');
function local(origin) {
  const h = new URL(origin).hostname.toLowerCase().replace(/^\[|\]$/g, '');
  return h === 'localhost' || h === '::1' || h === '0.0.0.0' || /^127\./.test(h) || h.startsWith('::ffff:127.');
}
function networkLog(role,event,destination='',code='') {
  try {
    let origin=''; try {const u=new URL(destination);origin=u.protocol+'//'+u.host;} catch {}
    const root=process.env.WBP_PORTABLE_ROOT;if(!root)return;
    const dir=path.join(root,'Data','Logs');fs.mkdirSync(dir,{recursive:true});
    fs.appendFileSync(path.join(dir,'network.log'),JSON.stringify({time:new Date().toISOString(),role,event,origin,code:String(code).replace(/[^A-Za-z0-9_.-]/g,'').slice(0,64)})+'\n','utf8');
  } catch {}
}
function install(role='daemon',injectedUndici) {
  if(globalThis.__wbPortable)return globalThis.__wbPortable;
  const endpoint=process.env.WBP_PORTABLE_PROXY, token=process.env.WBP_PORTABLE_PROXY_TOKEN;
  if(!endpoint||!token)throw Error('Use WorkBuddy-Portable.exe; portable proxy is missing');
  const p=new URL(endpoint);
  if(p.protocol!=='http:'||p.hostname!=='127.0.0.1'||!p.port||p.username||p.password||p.pathname!=='/'||p.search||p.hash)throw Error('Invalid portable proxy endpoint');
  const undici=injectedUndici??require('undici');
  const authenticated=new URL(endpoint);authenticated.username='wbp';authenticated.password=token;
  const authenticatedURL=authenticated.href;
  const ca=new Set(tls.rootCertificates);
  if(tls.getCACertificates)for(const type of ['default','system','extra']){
    try{for(const c of tls.getCACertificates(type))ca.add(c)}catch{}
  }
  const trust={ca:[...ca]};
  // This transport owns only its own agents. Keep custom per-request dispatchers
  // (including DNS-pinned security transports), certificate checks and aborts.
  const route=new undici.Agent({allowH2:false,factory(origin,options){
    return local(origin)?new undici.Pool(origin,{...options,allowH2:false,connect:{...options.connect,...trust}}):
      new undici.ProxyAgent({uri:endpoint,allowH2:false,requestTls:{...trust,allowH2:false},proxyTls:trust,token:'Basic '+Buffer.from('wbp:'+token).toString('base64')});
  }});
  const nativeFetch=globalThis.fetch.bind(globalThis);
  globalThis.fetch=async function portableFetch(input,init){
    if(init?.dispatcher)return nativeFetch(input,init);
    const destination=typeof input==='string'||input instanceof URL?String(input):input.url;
    networkLog(role,'fetch-start',destination);
    try {const r=await nativeFetch(input,{...init,dispatcher:route});networkLog(role,'fetch-response',destination,r.status);return r}
    catch(error){networkLog(role,'fetch-error',destination,error?.cause?.code??error?.code??'UNKNOWN');throw error}
  };
  const api={active:true,
    resolveProxyForUrl(target){return local(target)?{proxyUrl:undefined,rawRule:'[portable-loopback]'}:{proxyUrl:authenticatedURL,rawRule:'[portable-launcher]'}},
    installDispatcher(){undici.setGlobalDispatcher(route)},
    env(){return {HTTP_PROXY:authenticatedURL,HTTPS_PROXY:authenticatedURL,NO_PROXY:'localhost,127.0.0.1,::1,[::1]'}},
    async close(){globalThis.fetch=nativeFetch;await route.close()}
  };
  globalThis.__wbPortable=api;api.installDispatcher();networkLog(role,'proxy-installed');return api;
}
module.exports={install,local,networkLog};
