import fs from 'node:fs';
import path from 'node:path';
import {randomBytes} from 'node:crypto';
export function installPortableUpdate({app,dialog},run,nonce,api){
  let pending;
  async function request(){
    const id=randomBytes(12).toString('hex');
    const file=path.join(run,'update-check.request.json');
    fs.writeFileSync(file+'.new',JSON.stringify({nonce,id}),'utf8');fs.renameSync(file+'.new',file);
    const until=Date.now()+110000;
    while(Date.now()<until){
      await new Promise(resolve=>setTimeout(resolve,250));
      try {const r=JSON.parse(fs.readFileSync(path.join(run,'update-check.response.json'),'utf8'));if(r.nonce===nonce&&r.id===id){if(r.error)throw Object.assign(Error(r.error),{portableResult:true});return r}}
      catch(error){if(error.portableResult)throw error}
    }
    throw Error('更新检查超时，请查看 Data/Logs/network.log。');
  }
  api.checkUpdate=async(service,explicit=false)=>{
    if(service.checkInProgress)return;
    service.checkInProgress=true;service.setLastExplicit(explicit);service.setState('checking');
    try {
      const result=await(pending??=request().finally(()=>{pending=undefined}));
      const current=process.env.WBP_PORTABLE_VERSION;
      if(result.version===current){service.setState('idle');if(explicit)await dialog.showMessageBox({type:'info',title:'WorkBuddy 便携版更新',message:'已是已审核的官方当前构建：'+result.version+'\n本次检查使用便携启动器的代理。',buttons:['确定']});return}
      // Never claim an unreviewed future installer is compatible with this ASAR patch.
      throw Error('发现官方构建 '+result.version+'。请先更新便携启动器；不运行安装器、不覆盖当前运行时。');
    } catch(error){
      service.setState('error',undefined,undefined,{message:error.message,code:'PORTABLE_UPDATE'});
      if(explicit)await dialog.showMessageBox({type:'warning',title:'WorkBuddy 便携版更新',message:error.message,buttons:['确定']});
    } finally {service.checkInProgress=false}
  };
  api.installUpdate=service=>api.checkUpdate(service,true);
}
