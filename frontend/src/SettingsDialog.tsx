import { useTooltipDelay, useTooltipTimeout } from "./TooltipDelay"
import { toast } from "@/components/ui/toast"
import { useEffect, useRef, useState } from "react"
import { FolderOpenIcon, UploadIcon, EyeIcon, EyeOffIcon, SaveIcon, PlugIcon, GitCompareIcon, DownloadIcon } from "lucide-react"
import { Switch } from "@/components/ui/switch"
import { BackupService, SettingsService, BrowserService, ClipboardService } from "../bindings/quick-paste"
import type { BackupConfig, BackupState, BackupPreview, ClipboardLimits, ClipboardStats } from "../bindings/quick-paste"
import { BackupDiffDialog } from "./BackupDiffDialog"
import { Button } from "@/components/ui/button"
import { Dialog, DialogContent, DialogDescription, DialogTitle } from "@/components/ui/dialog"
import { Input } from "@/components/ui/input"
import { Field, FieldContent, FieldDescription, FieldGroup, FieldLabel } from "@/components/ui/field"
import { ScrollArea } from "@/components/ui/scroll-area"

const defaults: BackupConfig = { server: "", username: "", automatic: false, intervalHours: 6 }
const defaultClipboardLimits: ClipboardLimits = { maxItems: 10000, maxBytesMB: 64, textItemMB: 2, imageItemMB: 16 }
const formatDate = (value: string) => value ? new Date(value).toLocaleString() : "未记录"
const formatBytes = (value: number) => {
 const units=["B","KB","MB","GB"]
 let size=value,unit=0
 while(size>=1024&&unit<units.length-1){size/=1024;unit++}
 return `${unit===0||size>=10?Math.round(size):size.toFixed(1)} ${units[unit]}`
}
function Toggle({checked,disabled,onChange,label}: {checked:boolean;disabled:boolean;onChange:(value:boolean)=>void;label:string}) {
 return <Switch checked={checked} disabled={disabled} onCheckedChange={onChange} aria-label={label} />
}
export function SettingsDialog({open,onOpenChange,onRestored}: {open:boolean;onOpenChange:(value:boolean)=>void;onRestored:()=>Promise<void>}) {
 const tooltipDelay=useTooltipDelay()
 const tooltipTimeout=useTooltipTimeout()
 const [delayDraft,setDelayDraft]=useState(String(tooltipDelay))
 const [timeoutDraft,setTimeoutDraft]=useState(String(tooltipTimeout))
 useEffect(()=>{if(open){setDelayDraft(String(tooltipDelay));setTimeoutDraft(String(tooltipTimeout))}},[open,tooltipDelay,tooltipTimeout])
 const delayValid=delayDraft.trim()!=="" && Number.isInteger(Number(delayDraft)) && Number(delayDraft)>=0 && Number(delayDraft)<=5000
 const timeoutValid=timeoutDraft.trim()!=="" && Number.isInteger(Number(timeoutDraft)) && Number(timeoutDraft)>=0 && Number(timeoutDraft)<=5000
 const [tab,setTab]=useState("general")
 const [loading,setLoading]=useState(true)
 const [autostart,setAutostart]=useState(false)
 const [webViewIdleMinutes,setWebViewIdleMinutes]=useState(5)
 const [webViewIdleDraft,setWebViewIdleDraft]=useState("5")
 const [inputIntervalMs,setInputIntervalMs]=useState(0)
 const [inputIntervalDraft,setInputIntervalDraft]=useState("0")
 const inputIntervalValid=inputIntervalDraft.trim()!==""&&Number.isInteger(Number(inputIntervalDraft))&&Number(inputIntervalDraft)>=0&&Number(inputIntervalDraft)<=1000
 const [hotkey,setHotkey]=useState("")
 const [savedHotkey,setSavedHotkey]=useState("")
 const [hotkeyError,setHotkeyError]=useState("")
 const [clipboardEnabled,setClipboardEnabled]=useState(false)
 const [clipboardHotkey,setClipboardHotkey]=useState("")
 const [savedClipboardHotkey,setSavedClipboardHotkey]=useState("")
 const [clipboardHotkeyError,setClipboardHotkeyError]=useState("")
 const [winVTakeover,setWinVTakeover]=useState(false)
 const [clipboardLimits,setClipboardLimits]=useState<ClipboardLimits>(defaultClipboardLimits)
 const [savedClipboardLimits,setSavedClipboardLimits]=useState<ClipboardLimits>(defaultClipboardLimits)
 const [clipboardStats,setClipboardStats]=useState<ClipboardStats>({items:0,pinned:0,bytes:0})
 const [state,setState]=useState<BackupState|null>(null)
 const [config,setConfig]=useState<BackupConfig>(defaults)
 const [password,setPassword]=useState("")
 const [savedPassword,setSavedPassword]=useState("")
 const [showPassword,setShowPassword]=useState(false)
 const [busyActions,setBusyActions]=useState<Set<string>>(()=>new Set())
 const pending=useRef(new Set<string>())
 const [preview,setPreview]=useState<BackupPreview|null>(null)
 const [diffOpen,setDiffOpen]=useState(false)
 const [confirmRestore,setConfirmRestore]=useState(false)
 const dirty=!!state && (config.server!==state.server || config.username!==state.username || config.automatic!==state.automatic || config.intervalHours!==state.intervalHours || password!==savedPassword)
 const connected=!!state?.server && !dirty
 const isBusy=(key:string)=>busyActions.has(key)
 useEffect(()=>{if(!open)setLoading(true)},[open])
 useEffect(()=>{
  if(!open)return
  let active=true
  setLoading(true);setPreview(null);setDiffOpen(false);setConfirmRestore(false);setTab("general")
  const generalRequest=Promise.all([SettingsService.GetAutostart(),SettingsService.GetWebViewIdleMinutes(),SettingsService.GetInputIntervalMs(),BackupService.GetState(),BackupService.GetPassword()]).then(([enabled,idleMinutes,intervalMs,value,currentPassword])=>{
   if(!active)return
   setAutostart(enabled);setWebViewIdleMinutes(idleMinutes);setWebViewIdleDraft(String(idleMinutes));setInputIntervalMs(intervalMs);setInputIntervalDraft(String(intervalMs));setState(value);setConfig({server:value.server,username:value.username,automatic:value.automatic,intervalHours:value.intervalHours});setPassword(currentPassword);setSavedPassword(currentPassword);setShowPassword(false)
  }).catch(error=>{if(active)toast.add({type:"error",title:"操作失败",description:String(error)})})
  const hotkeyRequest=Promise.all([BrowserService.GetHotkey(),BrowserService.GetHotkeyError()]).then(([value,error])=>{if(active){setHotkey(value);setSavedHotkey(value);setHotkeyError(error)}}).catch(error=>{if(active)setHotkeyError(String(error))})
  const clipboardRequest=Promise.all([ClipboardService.GetEnabled(),BrowserService.GetClipboardHotkey(),BrowserService.GetClipboardHotkeyError(),BrowserService.GetWinVTakeover(),ClipboardService.GetLimits(),ClipboardService.GetStats()]).then(([enabled,value,error,takeover,limits,stats])=>{if(active){setClipboardEnabled(enabled);setClipboardHotkey(value);setSavedClipboardHotkey(value);setClipboardHotkeyError(error);setWinVTakeover(takeover);setClipboardLimits(limits);setSavedClipboardLimits(limits);setClipboardStats(stats)}}).catch(error=>{if(active)setClipboardHotkeyError(String(error))})
  void Promise.allSettled([generalRequest,hotkeyRequest,clipboardRequest]).then(()=>{if(active)setLoading(false)})
  const timer=setInterval(()=>{void BackupService.GetState().then(value=>{if(active)setState(value)}).catch(()=>{});void ClipboardService.GetStats().then(value=>{if(active)setClipboardStats(value)}).catch(()=>{})},3000)
  return ()=>{active=false;clearInterval(timer)}
 },[open])
 const run=async(key:string,action:()=>Promise<void>)=>{
  if(pending.current.has(key))return
  pending.current.add(key);setBusyActions(current=>new Set(current).add(key))
  try {await action()}catch(error){toast.add({type:"error",title:"操作失败",description:String(error)})}finally{
   try{setState(await BackupService.GetState())}catch{}
   pending.current.delete(key)
   setBusyActions(current=>{const next=new Set(current);next.delete(key);return next})
  }
 }
 return <>
  <Dialog open={open} onOpenChange={onOpenChange}>
   <DialogContent className="flex h-[680px] max-h-[calc(100svh-2rem)] max-w-4xl flex-col gap-0 p-0">
   <div className="shrink-0 border-b px-6 py-4"><DialogTitle className="text-base font-medium">设置</DialogTitle><DialogDescription className="sr-only">常规设置、WebDAV 备份与恢复</DialogDescription></div>
   <div className="flex min-h-0 flex-1">
    <nav aria-label="设置分类" className="w-36 shrink-0 space-y-1 border-r bg-muted/30 p-2">
     {[["general","常规"],["clipboard","剪贴板"],["webdav","WebDAV"]].map(([key,label])=><button key={key} type="button" aria-current={tab===key ? "page" : undefined} onClick={()=>setTab(key)} className={`w-full rounded-md px-3 py-2 text-left text-sm ${tab===key ? "bg-accent font-medium" : "hover:bg-accent"}`}>{label}</button>)}
    </nav>
    {loading?<div className="min-w-0 flex-1"/>:<ScrollArea className="min-w-0 flex-1">
     <div className="p-6">
     {tab==="general" && <div className="space-y-7">
      <div className="flex items-center justify-between gap-4"><label className="text-sm font-medium">开机自启</label><Toggle label="开机自启" checked={autostart} disabled={isBusy("autostart") || !state} onChange={value=>void run("autostart",async()=>{await SettingsService.SetAutostart(value);setAutostart(value)})} /></div>
       <FieldGroup>
        <Field orientation="horizontal" data-invalid={webViewIdleDraft.trim()==="" || !Number.isInteger(Number(webViewIdleDraft)) || Number(webViewIdleDraft)<1 || Number(webViewIdleDraft)>1440}>
         <FieldContent>
          <FieldLabel htmlFor="webview-idle-minutes">界面空闲回收</FieldLabel>
          <FieldDescription>剪贴板、快捷短语和补全面板隐藏一段时间后释放 WebView，默认 5 分钟。</FieldDescription>
         </FieldContent>
         <div className="flex items-center gap-2">
          <Input id="webview-idle-minutes" type="number" min={1} max={1440} step={1} className="w-24" value={webViewIdleDraft} aria-invalid={webViewIdleDraft.trim()==="" || !Number.isInteger(Number(webViewIdleDraft)) || Number(webViewIdleDraft)<1 || Number(webViewIdleDraft)>1440} disabled={isBusy("webview-idle-minutes")} onChange={event=>setWebViewIdleDraft(event.target.value)} onKeyDown={event=>{if(event.key==="Enter")event.currentTarget.blur()}} onBlur={()=>{
           const next=Number(webViewIdleDraft)
           if(webViewIdleDraft.trim()==="" || !Number.isInteger(next) || next<1 || next>1440){setWebViewIdleDraft(String(webViewIdleMinutes));return}
           if(next!==webViewIdleMinutes)void run("webview-idle-minutes",async()=>{try{await SettingsService.SetWebViewIdleMinutes(next);setWebViewIdleMinutes(next)}catch(error){setWebViewIdleDraft(String(webViewIdleMinutes));throw error}})
          }} />
          <span className="text-xs text-muted-foreground">分钟</span>
         </div>
       </Field>
       <Field orientation="horizontal" data-invalid={!inputIntervalValid}>
        <FieldContent>
         <FieldLabel htmlFor="input-interval-ms">短语输入间隔</FieldLabel>
         <FieldDescription>0 为快速输入；输入异常时可尝试 30–100 毫秒。</FieldDescription>
        </FieldContent>
        <div className="flex items-center gap-2">
         <Input id="input-interval-ms" type="number" min={0} max={1000} step={10} className="w-24" value={inputIntervalDraft} aria-invalid={!inputIntervalValid} disabled={isBusy("input-interval-ms")} onChange={event=>setInputIntervalDraft(event.target.value)} onKeyDown={event=>{if(event.key==="Enter")event.currentTarget.blur()}} onBlur={()=>{
          if(!inputIntervalValid){setInputIntervalDraft(String(inputIntervalMs));return}
          const next=Number(inputIntervalDraft)
          if(next!==inputIntervalMs)void run("input-interval-ms",async()=>{try{await SettingsService.SetInputIntervalMs(next);setInputIntervalMs(next)}catch(error){setInputIntervalDraft(String(inputIntervalMs));throw error}})
         }} />
         <span className="text-xs text-muted-foreground">毫秒</span>
        </div>
       </Field>
       </FieldGroup>
      <section className="space-y-3 border-t pt-5">
       <label htmlFor="browser-hotkey" className="text-sm font-medium">短语浏览快捷键</label>
       <Input id="browser-hotkey" value={hotkey} readOnly placeholder="点击后按下快捷键" onKeyDown={event=>{
        if(event.key==="Tab")return
        event.preventDefault()
        if(event.key==="Backspace" || event.key==="Delete"){setHotkey("");return}
        const key=event.code.startsWith("Key") ? event.code.slice(3) : event.code.startsWith("Digit") ? event.code.slice(5) : event.code==="Space" ? "Space" : /^F([1-9]|1[0-9]|2[0-4])$/.test(event.key) ? event.key : ""
        if(!key || !(event.ctrlKey||event.altKey||event.shiftKey||event.metaKey))return
        setHotkey([...event.ctrlKey?["Ctrl"]:[],...event.altKey?["Alt"]:[],...event.shiftKey?["Shift"]:[],...event.metaKey?["Win"]:[],key].join("+"))
       }} />
       {hotkeyError && <p role="alert" className="text-xs text-destructive">{hotkeyError}</p>}
       <div className="flex gap-2"><Button disabled={isBusy("hotkey") || (hotkey===savedHotkey && !hotkeyError)} onClick={()=>void run("hotkey",async()=>{await BrowserService.SetHotkey(hotkey);setSavedHotkey(hotkey);setHotkeyError("")})}>保存快捷键</Button><Button variant="outline" onClick={()=>setHotkey("")}>清空</Button></div>
      </section>
      <div className="flex items-center justify-between gap-4 border-t pt-5">
       <label htmlFor="tooltip-delay" className="text-sm font-medium">详情提示延迟</label>
       <div className="flex items-center gap-2"><Input id="tooltip-delay" type="number" min={0} max={5000} step={50} className="w-24" value={delayDraft} aria-invalid={!delayValid} disabled={isBusy("tooltip-delay")} onChange={event=>setDelayDraft(event.target.value)} onKeyDown={event=>{if(event.key==="Enter")event.currentTarget.blur()}} onBlur={()=>{
        if(!delayValid){setDelayDraft(String(tooltipDelay));return}
        const next=Number(delayDraft)
        if(next!==tooltipDelay)void run("tooltip-delay",async()=>{try{await SettingsService.SetTooltipDelay(next)}catch(error){setDelayDraft(String(tooltipDelay));throw error}})
       }} /><span className="text-xs text-muted-foreground">ms</span></div>
      </div>
      <div className="flex items-center justify-between gap-4">
       <label htmlFor="tooltip-timeout" className="text-sm font-medium">详情提示免延迟时间</label>
       <div className="flex items-center gap-2"><Input id="tooltip-timeout" type="number" min={0} max={5000} step={50} className="w-24" value={timeoutDraft} aria-invalid={!timeoutValid} disabled={isBusy("tooltip-timeout")} onChange={event=>setTimeoutDraft(event.target.value)} onKeyDown={event=>{if(event.key==="Enter")event.currentTarget.blur()}} onBlur={()=>{
        if(!timeoutValid){setTimeoutDraft(String(tooltipTimeout));return}
        const next=Number(timeoutDraft)
        if(next!==tooltipTimeout)void run("tooltip-timeout",async()=>{try{await SettingsService.SetTooltipTimeout(next)}catch(error){setTimeoutDraft(String(tooltipTimeout));throw error}})
       }} /><span className="text-xs text-muted-foreground">ms</span></div>
      </div>
      <section className="space-y-3 border-t pt-5"><h2 className="text-sm font-medium">本地数据</h2><code className="block select-text break-all rounded-md bg-muted p-3 text-xs">{state?.dataPath}</code><Button variant="outline" disabled={isBusy("open-dir") || !state} onClick={()=>void run("open-dir",async()=>{await BackupService.OpenDataDirectory()})}><FolderOpenIcon />打开目录</Button></section>
     </div>}
     {tab==="clipboard" && <div className="space-y-6">
      <div className="flex items-start justify-between gap-4"><div><h2 className="text-sm font-medium">启用剪贴板</h2><p className="mt-1 text-xs text-muted-foreground">剪贴板内容仅保存在本机，不参与备份。</p></div><Toggle label="启用剪贴板" checked={clipboardEnabled} disabled={isBusy("clipboard-enabled")} onChange={value=>void run("clipboard-enabled",async()=>{await ClipboardService.SetEnabled(value);setClipboardEnabled(value)})}/></div>
      <section className="space-y-3 border-t pt-5">
       <label htmlFor="clipboard-hotkey" className="text-sm font-medium">剪贴板快捷键</label>
       <Input id="clipboard-hotkey" value={clipboardHotkey} readOnly placeholder="点击后按下快捷键" onKeyDown={event=>{
        if(event.key==="Tab")return
        event.preventDefault()
        if(event.key==="Backspace"||event.key==="Delete"){setClipboardHotkey("");return}
        const key=event.code.startsWith("Key")?event.code.slice(3):event.code.startsWith("Digit")?event.code.slice(5):event.code==="Space"?"Space":/^F([1-9]|1[0-9]|2[0-4])$/.test(event.key)?event.key:""
        if(!key||!(event.ctrlKey||event.altKey||event.shiftKey||event.metaKey))return
        setClipboardHotkey([...event.ctrlKey?["Ctrl"]:[],...event.altKey?["Alt"]:[],...event.shiftKey?["Shift"]:[],...event.metaKey?["Win"]:[],key].join("+"))
       }}/>
       {clipboardHotkeyError&&<p role="alert" className="text-xs text-destructive">{clipboardHotkeyError}</p>}
       <div className="flex gap-2"><Button disabled={isBusy("clipboard-hotkey")||(clipboardHotkey===savedClipboardHotkey&&!clipboardHotkeyError)} onClick={()=>void run("clipboard-hotkey",async()=>{await BrowserService.SetClipboardHotkey(clipboardHotkey);setSavedClipboardHotkey(clipboardHotkey);setClipboardHotkeyError("")})}>保存快捷键</Button><Button variant="outline" onClick={()=>setClipboardHotkey("")}>清空</Button></div>
       <div className="flex items-center justify-between gap-4"><span className="text-sm">接管 Win + V</span><Toggle label="接管 Win + V" checked={winVTakeover} disabled={isBusy("win-v-takeover")} onChange={value=>void run("win-v-takeover",async()=>{await BrowserService.SetWinVTakeover(value);setWinVTakeover(value)})}/></div>
      </section>
      <section className="space-y-3 border-t pt-5">
       <h2 className="text-sm font-medium">存储上限</h2>
        <div className="space-y-2 rounded-md border bg-muted/30 px-3 py-2 text-xs">
         <div className="flex items-center justify-between gap-4"><span className="text-muted-foreground">当前占用</span><span>{formatBytes(clipboardStats.bytes)} / {clipboardLimits.maxBytesMB} MB</span></div>
         <div className="flex items-center justify-between gap-4"><span className="text-muted-foreground">记录数量</span><span className="flex items-center divide-x divide-border"><span className="pr-3 tabular-nums">{clipboardStats.items.toLocaleString()} 条</span><span className="pl-3 tabular-nums">{clipboardStats.pinned.toLocaleString()} 条置顶</span></span></div>
        </div>
       <div className="grid grid-cols-2 gap-4">
        {([
         ["clipboard-max-items","最大未置顶记录数","maxItems",100,100000,"条"],
         ["clipboard-max-bytes","总占用清理阈值","maxBytesMB",16,4096,"MB"],
         ["clipboard-text-item","文本/富文本/文件列表单项","textItemMB",1,64,"MB"],
         ["clipboard-image-item","图片单项","imageItemMB",1,256,"MB"],
        ] as const).map(([id,label,key,min,max,unit])=><div key={id} className="space-y-2"><label htmlFor={id} className="text-xs text-muted-foreground">{label}</label><div className="flex items-center gap-2"><Input id={id} type="number" min={min} max={max} step={1} value={clipboardLimits[key]} onChange={event=>setClipboardLimits({...clipboardLimits,[key]:Number(event.target.value)})}/><span className="w-7 text-xs text-muted-foreground">{unit}</span></div></div>)}
       </div>
       <p className="text-xs text-muted-foreground">超限时清理最旧未置顶项；置顶项保留。</p>
       <Button disabled={isBusy("clipboard-limits")||JSON.stringify(clipboardLimits)===JSON.stringify(savedClipboardLimits)} onClick={()=>void run("clipboard-limits",async()=>{await ClipboardService.SetLimits(clipboardLimits);setSavedClipboardLimits(clipboardLimits);setClipboardStats(await ClipboardService.GetStats())})}>保存上限</Button>
      </section>
      <section className="space-y-3 border-t pt-5"><h2 className="text-sm font-medium">历史管理</h2><div className="flex gap-2"><Button variant="outline" disabled={isBusy("clear-clipboard")} onClick={()=>void run("clear-clipboard",async()=>{await ClipboardService.Clear(false);setClipboardStats(await ClipboardService.GetStats())})}>清空未置顶</Button><Button disabled={isBusy("clear-all-clipboard")} onClick={()=>void run("clear-all-clipboard",async()=>{await ClipboardService.Clear(true);setClipboardStats(await ClipboardService.GetStats())})}>清空全部</Button></div></section>
     </div>}
     {tab==="webdav" && <form className="space-y-5" onSubmit={event=>{event.preventDefault();void run("save",async()=>{await BackupService.SaveConfig(config,password);const next=await BackupService.GetState();setState(next);setSavedPassword(password);setPreview(null);setDiffOpen(false)})}}>
      <div className="space-y-2"><label htmlFor="dav-server" className="text-sm font-medium">服务器目录地址</label><Input id="dav-server" type="url" value={config.server} onChange={e=>setConfig({...config,server:e.target.value})} placeholder="https://example.com/dav/" /><p className="text-xs text-muted-foreground">保存路径：quick-paste/backup.json</p></div>
      <div className="grid grid-cols-2 gap-4"><div className="space-y-2"><label htmlFor="dav-user" className="text-sm font-medium">账号</label><Input id="dav-user" autoComplete="off" value={config.username} onChange={e=>setConfig({...config,username:e.target.value})} /></div><div className="space-y-2"><label htmlFor="dav-password" className="text-sm font-medium">密码</label><div className="relative"><Input id="dav-password" className="pr-10" type={showPassword ? "text" : "password"} autoComplete="new-password" value={password} onChange={e=>setPassword(e.target.value)} /><div className="absolute inset-y-0 right-1 flex items-center"><Button type="button" variant="ghost" size="icon-sm" aria-label={showPassword ? "隐藏密码" : "显示密码"} onClick={()=>setShowPassword(value=>!value)}>{showPassword ? <EyeOffIcon /> : <EyeIcon />}</Button></div></div></div></div>
      <div className="flex items-center justify-between gap-4 border-t pt-5"><label className="text-sm font-medium">自动备份</label><Toggle label="自动备份" checked={config.automatic} disabled={false} onChange={automatic=>setConfig({...config,automatic})} /></div>
      <div className="flex items-center gap-3"><label htmlFor="backup-hours" className="text-sm">每</label><Input id="backup-hours" aria-label="备份间隔小时" type="number" min={1} max={168} step={1} className="w-20" value={config.intervalHours} onChange={e=>setConfig({...config,intervalHours:Number(e.target.value)})} /><span className="text-sm">小时备份一次</span></div>
      <div className="flex flex-wrap gap-2"><Button type="submit" disabled={isBusy("save") || !dirty}><SaveIcon />保存配置</Button><Button type="button" variant="outline" disabled={isBusy("test") || !connected} onClick={()=>void run("test",async()=>{await BackupService.TestConnection();toast.add({type:"success",title:"连接成功"})})}><PlugIcon />测试连接</Button></div>
      <div className="space-y-1 border-t pt-4 text-xs text-muted-foreground"><p>最近备份：{state?.lastBackup ? formatDate(state.lastBackup) : "尚未备份"}</p><p>下次备份：{state?.automatic && state.nextBackup ? formatDate(state.nextBackup) : "未开启"}</p>{state?.lastError && <p role="alert" className="text-destructive">{state.lastError}</p>}</div>
       <div className="flex flex-wrap gap-2"><Button type="button" variant="outline" disabled={isBusy("diff") || !connected} onClick={()=>void run("diff",async()=>{const next=await BackupService.PreviewBackup();setPreview(next);setDiffOpen(true)})}><GitCompareIcon />查看差异</Button><Button type="button" variant="outline" disabled={isBusy("backup") || !connected} onClick={()=>void run("backup",async()=>{await BackupService.BackupNow();setPreview(null);setDiffOpen(false);toast.add({type:"success",title:"备份完成"})})}><UploadIcon />立即备份</Button><Button type="button" variant="outline" disabled={!connected} onClick={()=>setConfirmRestore(true)}><DownloadIcon />恢复备份</Button></div>
     </form>}
     </div>
    </ScrollArea>}
   </div>
   </DialogContent>
  </Dialog>
  <BackupDiffDialog preview={preview} open={diffOpen} onOpenChange={setDiffOpen} />
  <Dialog open={confirmRestore} onOpenChange={setConfirmRestore}>
   <DialogContent>
    <DialogTitle className="pr-8 text-base font-medium">恢复备份？</DialogTitle>
    <DialogDescription>将覆盖本地数据。</DialogDescription>
    <div className="flex justify-end gap-2">
     <Button type="button" variant="outline" onClick={()=>setConfirmRestore(false)}>取消</Button>
     <Button type="button" disabled={isBusy("restore")} onClick={()=>void run("restore",async()=>{await BackupService.RestoreBackup();await onRestored();setPreview(null);setDiffOpen(false);setConfirmRestore(false);toast.add({type:"success",title:"恢复完成"})})}>恢复</Button>
    </div>
   </DialogContent>
  </Dialog>
 </>
}
