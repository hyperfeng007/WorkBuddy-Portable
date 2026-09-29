
/* WorkBuddy Portable: scoped CLI resolver. No PAC credentials or secret logging. */
function __wbpCLIProxy(target) {
  const endpoint=process.env.WBP_PORTABLE_PROXY;
  const token=process.env.WBP_PORTABLE_PROXY_TOKEN;
  if(!endpoint||!token)throw Error('Portable CLI proxy configuration missing; restart WorkBuddy-Portable.exe');
  const proxy=new URL(endpoint);
  if(proxy.protocol!=='http:'||proxy.hostname!=='127.0.0.1'||!proxy.port||proxy.username||proxy.password||proxy.pathname!=='/'||proxy.search||proxy.hash)throw Error('Invalid portable CLI proxy');
  const noProxy='localhost,127.0.0.1,::1,[::1]';
  if(target){
    const h=new URL(target).hostname.toLowerCase().replace(/^\[|\]$/g,'');
    if(h==='localhost'||h==='::1'||h==='0.0.0.0'||/^127\./.test(h)||h.startsWith('::ffff:127.'))return {proxyUrl:undefined,noProxy,source:'env'};
  }
  proxy.username='wbp';proxy.password=token;
  // 'env', not 'system': never trigger upstream's TLS-disable/direct-retry fallback.
  return {proxyUrl:proxy.href,noProxy,source:'env'};
}
