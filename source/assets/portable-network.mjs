/* Launcher-owned Electron proxy, scoped authentication, no direct fallback. */
export function configureElectronProxy({app,session,net},endpoint,token,log=()=>{}) {
  const proxy=new URL(endpoint);
  if(proxy.protocol!=='http:'||proxy.hostname!=='127.0.0.1'||!proxy.port||proxy.username||proxy.password||!token)throw Error('Invalid local proxy');
  const authenticates=auth=>auth?.isProxy&&auth.host==='127.0.0.1'&&String(auth.port)===proxy.port;
  app.commandLine.appendSwitch('proxy-server',endpoint);
  app.commandLine.appendSwitch('proxy-bypass-list','localhost;127.0.0.1;[::1]');
  // Own just OUR loopback proxy challenge; do not call competing auth callbacks.
  const emit=app.emit;
  app.emit=function(event,...args){
    if(event==='login'&&authenticates(args[3])){args[0].preventDefault();args[4]('wbp',token);return true}
    return emit.call(this,event,...args);
  };
  const request=net.request.bind(net);
  net.request=(...args)=>{
    const r=request(...args),emit=r.emit;
    r.emit=function(event,...values){
      if(event==='login'&&authenticates(values[0])){values[1]('wbp',token);return true}
      return emit.call(this,event,...values);
    };return r;
  };
  const pinned=new WeakSet();
  const settings={mode:'fixed_servers',proxyRules:endpoint,proxyBypassRules:'localhost,127.0.0.1,[::1]'};
  function pin(s){
    if(!pinned.has(s)){const native=s.setProxy.bind(s);s.setProxy=()=>native({...settings});pinned.add(s)}
    return s.setProxy();
  }
  const fromPartition=session.fromPartition.bind(session);
  session.fromPartition=(...args)=>{const s=fromPartition(...args);pin(s).catch(()=>{log('electron','session-proxy-error');app.exit(1)});return s};
  app.on('session-created',s=>{pin(s).catch(()=>{log('electron','session-proxy-error');app.exit(1)})});
  const nativeReady=app.whenReady.bind(app);
  const ready=nativeReady().then(async()=>{await pin(session.defaultSession);log('electron','sessions-proxy-ready')});
  app.whenReady=()=>ready;
  return ready;
}
