import fs from 'node:fs';
import path from 'node:path';
import os from 'node:os';
import assert from 'node:assert/strict';
import {pathToFileURL,fileURLToPath} from 'node:url';
import {registerHooks,createRequire} from 'node:module';
const root=fs.mkdtempSync(path.join(os.tmpdir(),'WorkBuddy U盘 中文 & 空格 𠀀😀-'));
const appdir=path.join(root,'app'),run=path.join(root,'Data','Run','fixture');fs.mkdirSync(path.join(appdir,'main'),{recursive:true});fs.mkdirSync(run,{recursive:true});
for(const name of ['portable.mjs','portable-node.cjs','portable-network.mjs','portable-update.mjs'])fs.copyFileSync(fileURLToPath(new URL('../assets/'+name,import.meta.url)),path.join(appdir,name));
const mock=path.join(root,'electron.mjs');fs.writeFileSync(mock,`
import {EventEmitter} from 'node:events';
export const app=new EventEmitter();app.paths={};app.commandLine={appendSwitch(){}};app.setPath=(k,v)=>app.paths[k]=v;app.setAppLogsPath=()=>{};app.setAsDefaultProtocolClient=()=>{throw Error('host protocol write')};app.removeAsDefaultProtocolClient=app.setAsDefaultProtocolClient;app.setLoginItemSettings=()=>{throw Error('autostart write')};app.addRecentDocument=()=>{throw Error('recent document write')};
const ready=new Promise(resolve=>globalThis.releaseReady=resolve);app.whenReady=()=>ready;app.quit=()=>app.emit('will-quit');app.exit=code=>{throw Error('app.exit '+code)};
export const session={defaultSession:{async setProxy(){}},fromPartition(){return this.defaultSession}};
export const net={request:()=>new EventEmitter()};export const dialog={showErrorBox(_title,detail){throw Error(detail)},async showMessageBox(){return {response:0}}};
`);
fs.writeFileSync(path.join(appdir,'main','index.js'),`
const assert=require('node:assert/strict'),fs=require('node:fs'),path=require('node:path'),{EventEmitter}=require('node:events');
module.exports=(async()=>{
const {app}=await import('electron');assert(globalThis.__wbPortable?.active);assert.equal(app.paths.userData,path.join(process.env.WBP_PORTABLE_ROOT,'Data','WorkBuddy','app'));
assert.equal(app.setAsDefaultProtocolClient('workbuddy'),false);app.setLoginItemSettings({openAtLogin:true});app.addRecentDocument('C:/host');
globalThis.releaseReady();await app.whenReady();
function window(url){const w=new EventEmitter();w.webContents=new EventEmitter();w.webContents.isDestroyed=()=>false;w.webContents.isLoadingMainFrame=()=>false;w.webContents.getURL=()=>url;w.isDestroyed=()=>false;w.isVisible=()=>true;return w}
const ready=path.join(process.env.WBP_PORTABLE_RUN,'ready');let w=window('file:///app/renderer/splash.html');app.emit('browser-window-created',null,w);w.emit('show');assert(!fs.existsSync(ready));
w=window('file:///app/renderer/index.html');app.emit('browser-window-created',null,w);w.webContents.emit('did-finish-load');assert.equal(fs.readFileSync(ready,'utf8'),'nonce');globalThis.fixtureDone=true;app.quit();
})();
`);
Object.assign(process.env,{WBP_PORTABLE_ROOT:root,WBP_PORTABLE_RUN:run,WBP_PORTABLE_NONCE:'nonce',WBP_PORTABLE_PROXY:'http://127.0.0.1:9876',WBP_PORTABLE_PROXY_TOKEN:'fixture-secret'});
const require=createRequire(import.meta.url);const undici=require.resolve(path.join(process.env.WBP_TEST_RUNTIME,'node_modules','undici'));
const hook=registerHooks({resolve(specifier,ctx,next){if(specifier==='electron')return {url:pathToFileURL(mock).href,shortCircuit:true};if(specifier==='undici')return next(undici,ctx);return next(specifier,ctx)}});
try{
 await import(pathToFileURL(path.join(appdir,'portable.mjs')).href);
 await require(path.join(appdir,'main','index.js'));
 assert(globalThis.fixtureDone);await globalThis.__wbPortable.close();
 console.log('PASS: awaited ESM bootstrap, synchronous daemon-compatible network setup, Unicode paths, no pre-ready deadlock, host hooks suppressed, splash ignored, ready nonce correct');
}finally{hook.deregister();fs.rmSync(root,{recursive:true,force:true})}
