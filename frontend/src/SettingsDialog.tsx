import { useTooltipDelay, useTooltipTimeout } from "./TooltipDelay"
import { toast } from "@/components/ui/toast"
import { useEffect, useRef, useState } from "react"
import { FolderOpenIcon, UploadIcon, EyeIcon, EyeOffIcon, SaveIcon, PlugIcon, GitCompareIcon, DownloadIcon } from "lucide-react"
import { Switch } from "@base-ui/react/switch"
import { BackupService, SettingsService, BrowserService } from "../bindings/quick-phrase"
import type { BackupConfig, BackupState, BackupPreview } from "../bindings/quick-phrase"
import { BackupDiffDialog } from "./BackupDiffDialog"
import { Button } from "@/components/ui/button"
import { Dialog, DialogContent, DialogDescription, DialogTitle } from "@/components/ui/dialog"
import { Input } from "@/components/ui/input"
import { ScrollArea } from "@/components/ui/scroll-area"

const defaults: BackupConfig = { server: "", username: "", automatic: false, intervalHours: 6 }
const formatDate = (value: string) => value ? new Date(value).toLocaleString() : "未记录"
function Toggle({checked,disabled,onChange,label}: {checked:boolean;disabled:boolean;onChange:(value:boolean)=>void;label:string}) {
 return <Switch.Root checked={checked} disabled={disabled} onCheckedChange={onChange} aria-label={label}
  className="inline-flex h-5 w-9 shrink-0 items-center rounded-full bg-input p-0.5 outline-none data-checked:bg-primary focus-visible:ring-2 focus-visible:ring-ring data-disabled:opacity-50">
  <Switch.Thumb className="size-4 rounded-full bg-background shadow-sm transition-transform data-checked:translate-x-4" />
 </Switch.Root>
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
 const [autostart,setAutostart]=useState(false)
 const [hotkey,setHotkey]=useState("")
 const [savedHotkey,setSavedHotkey]=useState("")
 const [hotkeyError,setHotkeyError]=useState("")
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
 useEffect(()=>{
  if(!open)return
  let active=true
  setPreview(null);setDiffOpen(false);setConfirmRestore(false);setTab("general")
  void Promise.all([SettingsService.GetAutostart(),BackupService.GetState(),BackupService.GetPassword()]).then(([enabled,value,currentPassword])=>{
   if(!active)return
   setAutostart(enabled);setState(value);setConfig({server:value.server,username:value.username,automatic:value.automatic,intervalHours:value.intervalHours});setPassword(currentPassword);setSavedPassword(currentPassword);setShowPassword(false)
  }).catch(error=>{if(active)toast.add({type:"error",title:"操作失败",description:String(error)})})
  void Promise.all([BrowserService.GetHotkey(),BrowserService.GetHotkeyError()]).then(([value,error])=>{if(active){setHotkey(value);setSavedHotkey(value);setHotkeyError(error)}}).catch(error=>{if(active)setHotkeyError(String(error))})
  const timer=setInterval(()=>{void BackupService.GetState().then(value=>{if(active)setState(value)}).catch(()=>{})},3000)
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
     {[["general","常规"],["webdav","WebDAV"]].map(([key,label])=><button key={key} type="button" aria-current={tab===key ? "page" : undefined} onClick={()=>setTab(key)} className={`w-full rounded-md px-3 py-2 text-left text-sm ${tab===key ? "bg-accent font-medium" : "hover:bg-accent"}`}>{label}</button>)}
    </nav>
    <ScrollArea className="min-w-0 flex-1">
     <div className="p-6">
     {tab==="general" && <div className="space-y-7">
      <div className="flex items-center justify-between gap-4"><label className="text-sm font-medium">开机自启</label><Toggle label="开机自启" checked={autostart} disabled={isBusy("autostart") || !state} onChange={value=>void run("autostart",async()=>{await SettingsService.SetAutostart(value);setAutostart(value)})} /></div>
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
     {tab==="webdav" && <form className="space-y-5" onSubmit={event=>{event.preventDefault();void run("save",async()=>{await BackupService.SaveConfig(config,password);const next=await BackupService.GetState();setState(next);setSavedPassword(password);setPreview(null);setDiffOpen(false)})}}>
      <div className="space-y-2"><label htmlFor="dav-server" className="text-sm font-medium">服务器目录地址</label><Input id="dav-server" type="url" value={config.server} onChange={e=>setConfig({...config,server:e.target.value})} placeholder="https://example.com/dav/" /><p className="text-xs text-muted-foreground">保存路径：quick-phrase/backup.json</p></div>
      <div className="grid grid-cols-2 gap-4"><div className="space-y-2"><label htmlFor="dav-user" className="text-sm font-medium">账号</label><Input id="dav-user" autoComplete="off" value={config.username} onChange={e=>setConfig({...config,username:e.target.value})} /></div><div className="space-y-2"><label htmlFor="dav-password" className="text-sm font-medium">密码</label><div className="relative"><Input id="dav-password" className="pr-10" type={showPassword ? "text" : "password"} autoComplete="new-password" value={password} onChange={e=>setPassword(e.target.value)} /><div className="absolute inset-y-0 right-1 flex items-center"><Button type="button" variant="ghost" size="icon-sm" aria-label={showPassword ? "隐藏密码" : "显示密码"} onClick={()=>setShowPassword(value=>!value)}>{showPassword ? <EyeOffIcon /> : <EyeIcon />}</Button></div></div></div></div>
      <div className="flex items-center justify-between gap-4 border-t pt-5"><label className="text-sm font-medium">自动备份</label><Toggle label="自动备份" checked={config.automatic} disabled={false} onChange={automatic=>setConfig({...config,automatic})} /></div>
      <div className="flex items-center gap-3"><label htmlFor="backup-hours" className="text-sm">每</label><Input id="backup-hours" aria-label="备份间隔小时" type="number" min={1} max={168} step={1} className="w-20" value={config.intervalHours} onChange={e=>setConfig({...config,intervalHours:Number(e.target.value)})} /><span className="text-sm">小时备份一次</span></div>
      <div className="flex flex-wrap gap-2"><Button type="submit" disabled={isBusy("save") || !dirty}><SaveIcon />保存配置</Button><Button type="button" variant="outline" disabled={isBusy("test") || !connected} onClick={()=>void run("test",async()=>{await BackupService.TestConnection();toast.add({type:"success",title:"连接成功"})})}><PlugIcon />测试连接</Button></div>
      <div className="space-y-1 border-t pt-4 text-xs text-muted-foreground"><p>最近备份：{state?.lastBackup ? formatDate(state.lastBackup) : "尚未备份"}</p><p>下次备份：{state?.automatic && state.nextBackup ? formatDate(state.nextBackup) : "未开启"}</p>{state?.lastError && <p role="alert" className="text-destructive">{state.lastError}</p>}</div>
       <div className="flex flex-wrap gap-2"><Button type="button" variant="outline" disabled={isBusy("diff") || !connected} onClick={()=>void run("diff",async()=>{const next=await BackupService.PreviewBackup();setPreview(next);setDiffOpen(true)})}><GitCompareIcon />查看差异</Button><Button type="button" variant="outline" disabled={isBusy("backup") || !connected} onClick={()=>void run("backup",async()=>{await BackupService.BackupNow();setPreview(null);setDiffOpen(false);toast.add({type:"success",title:"备份完成"})})}><UploadIcon />立即备份</Button><Button type="button" variant="outline" disabled={!connected} onClick={()=>setConfirmRestore(true)}><DownloadIcon />恢复备份</Button></div>
     </form>}
     </div>
    </ScrollArea>
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
