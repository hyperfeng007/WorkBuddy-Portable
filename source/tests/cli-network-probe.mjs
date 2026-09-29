import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import vm from 'node:vm';
import https from 'node:https';
import {createRequire} from 'node:module';
const require=createRequire(import.meta.url);
const {HttpsProxyAgent}=require(path.join(process.env.WBP_TEST_RUNTIME,'node_modules/https-proxy-agent'));
const name=process.env.WBP_TEST_BASELINE==='cli-headless'?'codebuddy-headless.js':'codebuddy-lite-wb.mjs';
const original=fs.readFileSync(path.join(process.env.WBP_TEST_APP,'resources/app.asar.unpacked/cli/dist',name),'utf8');
const patched=fs.readFileSync(path.join(process.env.WBP_TEST_CLI_PATCHED,'cli/dist',name),'utf8');
function resolver(source){
 const start=source.indexOf('let ProxyResolver=class ProxyResolver');assert(start>=0);
 const end=source.indexOf('.ProxyResolver=ProxyResolver',start);assert(end>start);
 const realModule=source.slice(start,end).replace(/[A-Za-z_$][\w$]*$/,'');
 const helper=source.lastIndexOf('function __wbpCLIProxy(');
 const ctx={URL,process:{env:process.env}};vm.createContext(ctx);
 vm.runInContext(realModule+';globalThis.Resolver=ProxyResolver;'+(helper<0?'':source.slice(helper)),ctx);
 return new ctx.Resolver({osCacheTtlMs:1,osDetectTimeoutMs:1,settingsPath:'/not-used',log(){},resolveForUrl:async()=>`PROXY ${new URL(process.env.WBP_PORTABLE_PROXY).host}`});
}
function request(proxyUrl){
 const agent=new HttpsProxyAgent(proxyUrl);
 return new Promise((resolve,reject)=>{
  const req=https.get(process.env.WBP_TEST_ORIGIN+'/v1/models',{agent,headers:{Authorization:'Bearer fixture-model-key'}},res=>{
   let text='';res.setEncoding('utf8');res.on('data',v=>text+=v);res.on('end',()=>{agent.destroy();resolve({status:res.statusCode,text})});res.on('error',reject);
  });req.setTimeout(12000,()=>req.destroy(Error('request timeout')));req.on('error',e=>{agent.destroy();reject(e)});
 });
}
// The old resolver prefers Chromium PAC, discarding otherwise-correct env credentials.
process.env.HTTPS_PROXY=process.env.HTTP_PROXY=process.env.WBP_PORTABLE_PROXY.replace('http://','http://wbp:'+process.env.WBP_PORTABLE_PROXY_TOKEN+'@');
const before=await resolver(original).resolveForUrl(process.env.WBP_TEST_ORIGIN);
assert.equal(new URL(before.proxyUrl).username,'');
const failure=await request(before.proxyUrl);assert.equal(failure.status,407);assert.equal(failure.text,'Proxy authentication required\n');
console.log('REPRODUCED '+name+': original CLI PAC priority drops env credentials -> real bridge 407');
const fixed=resolver(patched);fixed.setResolveForUrlHook(()=>{throw Error('must not enter credential-losing PAC branch')});
const after=await fixed.resolveForUrl(process.env.WBP_TEST_ORIGIN);
assert.equal(after.source,'env');assert.equal(new URL(after.proxyUrl).password,process.env.WBP_PORTABLE_PROXY_TOKEN);
assert.equal(fixed.resolve().proxyUrl,after.proxyUrl);
assert.equal((await fixed.resolveForUrl('http://127.0.0.1:9485')).proxyUrl,undefined);
const response=await request(after.proxyUrl);assert.equal(response.status,200);assert.equal(JSON.parse(response.text).data[0].label,'中文内容 𠀀😀');
const wrong=new URL(after.proxyUrl);wrong.password='incorrect-token';assert.equal((await request(wrong.href)).status,407);
console.log('PASS '+name+': Go-patched CLI resolver -> authenticated bridge/upstream CONNECT -> TLS target 200; wrong token still rejected, sync resolver and loopback preserved');
