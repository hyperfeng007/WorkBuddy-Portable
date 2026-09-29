import assert from 'node:assert/strict';
import {createRequire} from 'node:module';
import path from 'node:path';
import portableNode from '../assets/portable-node.cjs';
const req=createRequire(import.meta.url);
const undici=req(path.join(process.env.WBP_TEST_RUNTIME,'node_modules','undici'));
assert.equal(req(path.join(process.env.WBP_TEST_RUNTIME,'node_modules','undici','package.json')).version,'6.25.0');
const target=process.env.WBP_TEST_ORIGIN+'/v1/models';
if(process.env.WBP_TEST_BASELINE==='1')await assert.rejects(fetch(target,{signal:AbortSignal.timeout(3000)}));
const api=portableNode.install('probe',undici);
const resolver=api.resolveProxyForUrl(target);
assert.equal(new URL(resolver.proxyUrl).username,'wbp');
assert.equal(new URL(resolver.proxyUrl).password,process.env.WBP_PORTABLE_PROXY_TOKEN);
assert(!resolver.rawRule.includes(process.env.WBP_PORTABLE_PROXY_TOKEN));
assert.equal(api.resolveProxyForUrl('http://127.0.0.1:1234').proxyUrl,undefined);
if(process.env.WBP_TEST_BASELINE==='reject'){
 await assert.rejects(fetch(target,{headers:{Authorization:'Bearer fixture-model-key'},signal:AbortSignal.timeout(15000)}),error=>/CERT|SELF_SIGNED/i.test(error?.cause?.code??''));
 await api.close();console.log('PASS: untrusted certificate rejected; TLS verification intact');process.exit(0);
}
const response=await fetch(target,{headers:{Authorization:'Bearer fixture-model-key'},signal:AbortSignal.timeout(15000)});
assert.equal(response.status,200);const data=await response.json();assert.equal(data.data[0].id,'fixture-model');assert.equal(data.data[0].label,'中文内容 𠀀😀');
assert.equal(await(await fetch(process.env.WBP_TEST_LOOPBACK)).text(),'local-ok');
// An intentional custom dispatcher must retain control, not be overwritten.
let customHit=false;
const custom={dispatch(_opts,handler){customHit=true;queueMicrotask(()=>handler.onError(Error('custom sentinel')));return true}};
await assert.rejects(fetch(target,{dispatcher:custom}),error=>error.cause?.message==='custom sentinel');assert(customHit);
await api.close();
console.log('PASS: actual bundled Undici 6.25, native fetch, authenticated launcher CONNECT, trusted extra CA, decompressed JSON, loopback bypass and caller dispatcher preservation');
