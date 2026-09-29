import test from 'node:test';
import assert from 'node:assert/strict';
import {EventEmitter} from 'node:events';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import vm from 'node:vm';
import {configureElectronProxy} from '../assets/portable-network.mjs';
import {installPortableUpdate} from '../assets/portable-update.mjs';

function mocks(){
 const app=new EventEmitter();app.commandLine={appendSwitch(){}};app.whenReady=()=>Promise.resolve();app.exit=()=>{throw Error('unexpected exit')};
 const make=()=>({calls:[],async setProxy(v){this.calls.push(v)}});
 const defaultSession=make(),other=make();const session={defaultSession,fromPartition:()=>other};
 const net={request:()=>new EventEmitter()};return {app,session,net,make,other};
}
test('Electron authenticates only its bridge, exclusively; no credentials sent to origin login',async()=>{
 const m=mocks();await configureElectronProxy(m,'http://127.0.0.1:9876','secret');let late=0;m.app.on('login',()=>late++);
 let credentials;const event={preventDefault(){}};
 m.app.emit('login',event,null,{}, {isProxy:true,host:'127.0.0.1',port:9876},(...v)=>credentials=v);
 assert.deepEqual(credentials,['wbp','secret']);assert.equal(late,0);
 m.app.emit('login',event,null,{}, {isProxy:false,host:'127.0.0.1',port:9876},()=>{throw Error('credentials leaked')});assert.equal(late,1);
 const request=m.net.request();let other=0;request.on('login',()=>other++);
 request.emit('login',{isProxy:true,host:'127.0.0.1',port:9876},(...v)=>assert.deepEqual(v,['wbp','secret']));assert.equal(other,0);
 request.emit('login',{isProxy:true,host:'127.0.0.1',port:1111},()=>{throw Error('credentials leaked')});assert.equal(other,1);
});
test('later proxy changes and new partitions remain launcher-owned',async()=>{
 const m=mocks();await configureElectronProxy(m,'http://127.0.0.1:9876','secret');await m.session.defaultSession.setProxy({mode:'direct'});
 const other=m.session.fromPartition('x');await other.setProxy({mode:'system'});const fresh=m.make();m.app.emit('session-created',fresh);await fresh.setProxy({mode:'direct'});
 for(const s of [m.session.defaultSession,other,fresh])for(const x of s.calls){assert.equal(x.mode,'fixed_servers');assert.equal(x.proxyRules,'http://127.0.0.1:9876');assert(!JSON.stringify(x).includes('secret'))}
});
const patched=process.env.WBP_TEST_OUTPUT;
function source(name){return fs.readFileSync(path.join(patched,'main',name),'utf8')}
test('actual patched PAC resolver retains authentication without exposing it in the PAC rule',{skip:!patched},async()=>{
 const s=source('proxy-agents.js'),a=s.indexOf('async function resolveProxyForUrl(targetUrl) {'),b=s.indexOf('\nasync function performResolve',a);
 const context={__wbPortable:{resolveProxyForUrl:u=>({proxyUrl:'http://wbp:token@127.0.0.1:99',rawRule:'[portable-launcher]'})},cacheKey(){throw Error('incorrect unauthenticated PAC path')}};
 vm.createContext(context);vm.runInContext(s.slice(a,b),context);const r=await context.resolveProxyForUrl('https://example.invalid');assert.equal(new URL(r.proxyUrl).password,'token');assert(!r.rawRule.includes('token'));
});
test('actual proxy settings service rejects before persistence or host application',{skip:!patched},async()=>{
 const s=source('server.js'),a=s.indexOf('var ProxySettingsService = class {'),b=s.indexOf('\n//#endregion',a);const context={__wbPortable:{}};vm.createContext(context);vm.runInContext(s.slice(a,b),context);
 const service=new context.ProxySettingsService(new Proxy({}, {get(){throw Error('must not touch settings or host')}}));
 assert.equal((await service.getSettings()).mode,'system');for(const mode of ['manual','none','system']){const r=await service.saveSettings({mode,url:'http://example.invalid'});assert.equal(r.ok,false);assert.equal(r.errorCode,'PORTABLE_MANAGED')}
});
test('actual Windows updater entry points cannot spawn/install the official installer',{skip:!patched},async()=>{
 const s=source('index.js'),a=s.indexOf('var UpdateServiceWin32 = class extends AbstractUpdateService {'),b=s.indexOf('\n//#endregion',a);let checks=0,installs=0;
 const context={AbstractUpdateService:class{},__wbPortable:{checkUpdate:()=>checks++,installUpdate:()=>installs++}};vm.createContext(context);vm.runInContext(s.slice(a,b),context);
 const service=Object.create(context.UpdateServiceWin32.prototype);await service.restoreDownloadedUpdate();assert.equal(service.getUpdateTargetSuffix(),'-user');await service.checkForUpdates(true);await service.downloadUpdate({});service.quitAndInstall();assert.equal(checks,2);assert.equal(installs,1);
});
test('desktop update request is nonce-bound, uses launcher, and keeps mandatory-upgrade metadata',async()=>{
 const run=fs.mkdtempSync(path.join(os.tmpdir(),'wb-update-'));const nonce='fixture-nonce';const api={};const messages=[];const service={forceUpgrade:{targetVersion:'required'},setState(state,...rest){this.state=state;this.rest=rest},setLastExplicit(v){this.explicit=v}};
 const old=process.env.WBP_PORTABLE_VERSION;process.env.WBP_PORTABLE_VERSION='5.6.2.39298511';
 const app={quit(){throw Error('must not quit or spawn updater')}};const dialog={async showMessageBox(v){messages.push(v);return {response:0}}};installPortableUpdate({app,dialog},run,nonce,api);
 const timer=setInterval(()=>{const p=path.join(run,'update-check.request.json');if(fs.existsSync(p)){const input=JSON.parse(fs.readFileSync(p));fs.unlinkSync(p);assert.equal(input.nonce,nonce);fs.writeFileSync(path.join(run,'update-check.response.json'),JSON.stringify({nonce,id:input.id,version:'5.6.2.39298511'}))}},20);
 try{await api.checkUpdate(service,true);assert.equal(service.state,'idle');assert.equal(service.forceUpgrade.targetVersion,'required');assert.equal(messages.length,1)}finally{clearInterval(timer);fs.rmSync(run,{recursive:true,force:true});if(old===undefined)delete process.env.WBP_PORTABLE_VERSION;else process.env.WBP_PORTABLE_VERSION=old}
});
