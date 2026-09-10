import { useEffect, useMemo, useRef, useState } from "react"
import type { DragEvent } from "react"
import {
  AppWindowIcon, CheckIcon, ClipboardIcon, CopyIcon, FileIcon, FileTextIcon, FolderIcon, FolderOpenIcon,
  FolderInputIcon, FolderPlusIcon, ImageIcon, PencilIcon, PinIcon, PinOffIcon, PlusIcon,
  SearchIcon, Trash2Icon, XIcon,
} from "lucide-react"
import { Events } from "@wailsio/runtime"
import { BrowserService, ClipboardService, PhraseService, type ClipboardItem, type PhraseNode } from "../bindings/quick-paste"
import { ScrollArea } from "@/components/ui/scroll-area"
import { Input } from "@/components/ui/input"
import { Textarea } from "@/components/ui/textarea"
import { Dialog, DialogContent, DialogTitle, DialogDescription } from "@/components/ui/dialog"
import { Select, SelectContent, SelectGroup, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { Field, FieldGroup, FieldLabel } from "@/components/ui/field"
import { Button } from "@/components/ui/button"
import { ContextMenu, ContextMenuContent, ContextMenuGroup, ContextMenuItem, ContextMenuSeparator, ContextMenuTrigger } from "@/components/ui/context-menu"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import { useTooltipDelay } from "./TooltipDelay"
import { canMove, childNodes, hasEnabledCompletionAncestor } from "./phrase-tree"
import { PhraseDestinationTree } from "./PhraseDestinationTree"

type Tab = "clipboard" | "phrase"
type Editor = { kind: "folder" | "phrase"; id?: string; parentId: string; name: string; content: string }

function relativeTime(value: string) {
  const seconds = Math.max(0, Math.floor((Date.now() - new Date(value).getTime()) / 1000))
  if (seconds < 60) return "刚刚"
  if (seconds < 3600) return `${Math.floor(seconds / 60)} 分钟前`
  if (seconds < 86400) return `${Math.floor(seconds / 3600)} 小时前`
  if (seconds < 86400 * 7) return `${Math.floor(seconds / 86400)} 天前`
  return new Date(value).toLocaleDateString()
}

function phrasePath(node: PhraseNode, nodes: PhraseNode[]) {
  const parts: string[] = []
  let parent = node.parentId ?? ""
  const seen = new Set<string>()
  while (parent && !seen.has(parent)) {
    seen.add(parent)
    const folder = nodes.find(item => item.id === parent && item.type === "folder")
    if (!folder) break
    parts.unshift(folder.name)
    parent = folder.parentId ?? ""
  }
  return parts.join(" / ") || "根目录"
}

function ClipboardThumbnail({id}: {id:string}) {
  const host=useRef<HTMLSpanElement>(null)
  const [src,setSrc]=useState("")
  useEffect(()=>{
    const element=host.current
    if(!element)return
    const observer=new IntersectionObserver(entries=>{
      if(!entries.some(entry=>entry.isIntersecting))return
      observer.disconnect()
      void ClipboardService.GetPreviewData(id).then(setSrc).catch(()=>{})
    })
    observer.observe(element)
    return ()=>observer.disconnect()
  },[id])
  return <span ref={host} className="flex h-full min-w-0 items-center justify-center overflow-hidden">{src&&<img src={src} alt="剪贴板图片预览" className="h-full max-h-24 w-full rounded-sm object-contain"/>}</span>
}

export function BrowserApp() {
  const tooltipDelay=useTooltipDelay()
  const [tab,setTab]=useState<Tab>("phrase")
  const [nodes,setNodes]=useState<PhraseNode[]>([])
  const [items,setItems]=useState<ClipboardItem[]>([])
  const [expanded,setExpanded]=useState<Set<string>>(()=>new Set())
  const [queries,setQueries]=useState<Record<Tab,string>>({clipboard:"",phrase:""})
  const [captureEnabled,setCaptureEnabled]=useState(false)
  const [pinned,setPinned]=useState(false)
  const [loading,setLoading]=useState(true)
  const [error,setError]=useState("")
  const [busy,setBusy]=useState(false)
  const [editor,setEditor]=useState<Editor|null>(null)
  const [contextId,setContextId]=useState<string|null>(null)
  const [transientVersion,setTransientVersion]=useState(0)
  const [draggedId,setDraggedId]=useState<string|null>(null)
  const [dropTarget,setDropTarget]=useState<string|null>(null)
  const [moveNode,setMoveNode]=useState<PhraseNode|null>(null)
  const [moveTarget,setMoveTarget]=useState("")
  const detailTimer=useRef<ReturnType<typeof setTimeout>|null>(null)
  const clearDetailTimer=()=>{if(detailTimer.current!==null){clearTimeout(detailTimer.current);detailTimer.current=null}}
  const [copiedId,setCopiedId]=useState<string|null>(null)
  const [undoVisible,setUndoVisible]=useState(false)
  const copiedTimer=useRef<number|undefined>(undefined)
  const searchRef=useRef<HTMLInputElement>(null)
  const clipboardQueryRef=useRef("")
  const clipboardSearchVersion=useRef(0)
  clipboardQueryRef.current=queries.clipboard

  const resetTransient=()=>{
    clearDetailTimer()
    window.clearTimeout(copiedTimer.current)
    setCopiedId(null);setUndoVisible(false);setContextId(null)
    setEditor(null);setMoveNode(null)
    setDraggedId(null);setDropTarget(null)
    setTransientVersion(current=>current+1)
    void BrowserService.DismissDetail()
  }

  const reloadPhrases=async()=>setNodes(await PhraseService.ListNodes() ?? [])
  const reloadClipboard=async()=>{
    const version=++clipboardSearchVersion.current
    const value=await ClipboardService.List(clipboardQueryRef.current)
    if(version===clipboardSearchVersion.current)setItems(value??[])
    setCaptureEnabled(await ClipboardService.GetEnabled())
  }
  useEffect(()=>{
    let active=true
    const refresh=(event?:{data?:unknown})=>{
      if(!active)return
      resetTransient()
      setLoading(true)
      const requested=event?.data === "clipboard" ? "clipboard" : event?.data === "phrase" ? "phrase" : null
      if(requested)setTab(requested)
      setQueries({clipboard:"",phrase:""});setError("")
      void Promise.all([reloadPhrases(),reloadClipboard()]).catch(e=>active&&setError(String(e))).finally(()=>{if(active)setLoading(false)})
    }
    const changed=()=>{if(active)void reloadClipboard().catch(e=>setError(String(e)))}
    const cancelRefresh=Events.On("browser-refresh",refresh)
    const cancelTab=Events.On("browser-tab",event=>setTab(event.data === "clipboard" ? "clipboard" : "phrase"))
    const cancelChanged=Events.On("clipboard-changed",changed)
    const cancelState=Events.On("clipboard-state-changed",changed)
    const cancelPhrases=Events.On("phrases-changed",()=>{if(active)void reloadPhrases().catch(e=>setError(String(e)))})
    const hidden=Events.On("browser-hidden",resetTransient)
    void BrowserService.GetActiveTab().then(value=>{if(active)setTab(value==="clipboard"?"clipboard":"phrase")}).catch(()=>{})
    void BrowserService.GetPinned().then(value=>{if(active)setPinned(value)}).catch(()=>{})
    refresh()
    return ()=>{active=false;cancelRefresh();cancelTab();cancelChanged();cancelState();cancelPhrases();hidden()}
  },[])
  useEffect(()=>{
    if(tab!=="clipboard")return
    const version=++clipboardSearchVersion.current
    const timer=setTimeout(()=>void ClipboardService.List(queries.clipboard).then(value=>{if(version===clipboardSearchVersion.current)setItems(value??[])}).catch(e=>setError(String(e))),120)
    return ()=>clearTimeout(timer)
  },[queries.clipboard,tab])
  useEffect(()=>()=>{window.clearTimeout(copiedTimer.current);clearDetailTimer()},[])
  useEffect(()=>{clearDetailTimer();void BrowserService.DismissDetail()},[tab,queries])
  useEffect(()=>{
    if(!loading)return
    const timer=window.setTimeout(()=>void BrowserService.ReadyToShow(),0)
    return ()=>window.clearTimeout(timer)
  },[loading,transientVersion])

  const run=async(action:()=>Promise<unknown>)=>{
    if(busy)return
    setBusy(true);setError("")
    try{await action()}catch(e){setError(String(e))}finally{setBusy(false)}
  }
  const insert=(id:string)=>run(()=>BrowserService.Insert(id))
  const paste=(id:string,plain=false)=>run(()=>ClipboardService.Paste(id,plain))
  const openEditor=(node?:PhraseNode,parentId="",kind:"folder"|"phrase"="phrase")=>{
    const next:Editor=node
      ? {kind:node.type==="folder"?"folder":"phrase",id:node.id,parentId:node.parentId??"",name:node.name,content:node.content??""}
      : {kind,parentId,name:"",content:""}
    setEditor(next)
  }
  const closeEditor=()=>setEditor(null)
  const saveEditor=()=>run(async()=>{
    if(!editor || !editor.name.trim())throw new Error("名称不能为空")
    if(editor.id){
      if(editor.kind==="folder")await PhraseService.RenameNode(editor.id,editor.name)
      else await PhraseService.UpdatePhrase(editor.id,editor.name,editor.content)
      if((nodes.find(node=>node.id===editor.id)?.parentId??"")!==editor.parentId)await PhraseService.MoveNode(editor.id,editor.parentId)
    }else if(editor.kind==="folder")await PhraseService.CreateNode(editor.parentId,"folder",editor.name)
    else await PhraseService.CreatePhraseWithContent(editor.parentId,editor.name,editor.content)
    setExpanded(current=>new Set(current).add(editor.parentId));setEditor(null);await reloadPhrases()
  })
  const addClipboardToPhrase=async(item:ClipboardItem)=>{
    try {
      const content=await ClipboardService.GetText(item.id)
      const next:Editor={kind:"phrase",parentId:"",name:item.kind==="files" ? item.preview : content.slice(0,30),content}
      setTab("phrase");setEditor(next)
    } catch(e) { setError(String(e)) }
  }

  const copyClipboardItem=(id:string)=>run(async()=>{
    await ClipboardService.Copy(id,false)
    setCopiedId(id)
    window.clearTimeout(copiedTimer.current)
    copiedTimer.current=window.setTimeout(()=>setCopiedId(current=>current===id?null:current),1500)
    await reloadClipboard()
  })

  const movePhraseNode=async(id:string,target:string)=>{
    const node=nodes.find(item=>item.id===id)
    if(!node||!canMove(nodes,id,target)||(node.parentId??"")===target)return
    await PhraseService.MoveNode(id,target)
    if(target)setExpanded(current=>new Set(current).add(target))
    await reloadPhrases()
  }
  const openMove=(node:PhraseNode)=>{setMoveNode(node);setMoveTarget(node.parentId??"")}
  const submitMove=()=>run(async()=>{
    if(!moveNode)return
    await movePhraseNode(moveNode.id,moveTarget)
    setMoveNode(null)
  })

  const phraseResults=useMemo(()=>{
    const query=queries.phrase.trim().toLocaleLowerCase()
    if(!query)return null
    return nodes.filter(node=>node.type==="phrase").map(node=>({node,path:phrasePath(node,nodes)})).filter(({node,path})=>
      `${node.name}\n${node.content??""}\n${path}`.toLocaleLowerCase().includes(query)
    ).sort((a,b)=>{
      const rank=(name:string)=>name===query?0:name.startsWith(query)?1:name.includes(query)?2:3
      return rank(a.node.name.toLocaleLowerCase())-rank(b.node.name.toLocaleLowerCase())||a.node.order-b.node.order
    })
  },[nodes,queries.phrase])

  const dragProps=(node:PhraseNode)=>({
    draggable:true,
    onDragStart:(event:DragEvent<HTMLButtonElement>)=>{
      event.stopPropagation()
      event.dataTransfer.setData("application/x-quick-paste-node",node.id)
      event.dataTransfer.effectAllowed="move"
      setDraggedId(node.id)
    },
    onDragEnd:()=>{setDraggedId(null);setDropTarget(null)},
    onDragOver:(event:DragEvent<HTMLButtonElement>)=>{
      if(node.type!=="folder"||!draggedId||!canMove(nodes,draggedId,node.id))return
      event.preventDefault();event.stopPropagation();event.dataTransfer.dropEffect="move";setDropTarget(node.id)
    },
    onDragLeave:(event:DragEvent<HTMLButtonElement>)=>{
      if(!event.currentTarget.contains(event.relatedTarget as Node|null))setDropTarget(current=>current===node.id?null:current)
    },
    onDrop:(event:DragEvent<HTMLButtonElement>)=>{
      event.preventDefault();event.stopPropagation()
      const id=event.dataTransfer.getData("application/x-quick-paste-node")||draggedId||""
      setDraggedId(null);setDropTarget(null)
      if(node.type==="folder"&&canMove(nodes,id,node.id))void run(()=>movePhraseNode(id,node.id))
    },
  })

  const phraseContext=(node:PhraseNode)=>{
    const completionInherited=node.type==="folder"&&hasEnabledCompletionAncestor(nodes,node)
    return <ContextMenuContent finalFocus={false}>
    <ContextMenuGroup>
      {node.type==="folder"&&<>
        <ContextMenuItem onClick={()=>openEditor(undefined,node.id,"phrase")}><PlusIcon/>新建短语</ContextMenuItem>
        <ContextMenuItem onClick={()=>openEditor(undefined,node.id,"folder")}><FolderPlusIcon/>新建文件夹</ContextMenuItem>
        <ContextMenuItem disabled={completionInherited} onClick={()=>void run(async()=>{await PhraseService.SetFolderCompletion(node.id,!node.completionEnabled);await reloadPhrases()})}>
          {node.completionEnabled||completionInherited?<CheckIcon/>:<span className="size-4"/>}{completionInherited?"自动补全（已继承）":"自动补全"}
        </ContextMenuItem>
        <ContextMenuSeparator/>
      </>}
      <ContextMenuItem onClick={()=>openEditor(node)}><PencilIcon/>{node.type==="folder"?"重命名":"编辑"}</ContextMenuItem>
      <ContextMenuItem onClick={()=>openMove(node)}><FolderInputIcon/>移动到</ContextMenuItem>
      <ContextMenuItem onClick={()=>void run(async()=>{await PhraseService.DeleteNode(node.id);await reloadPhrases()})}><Trash2Icon/>删除</ContextMenuItem>
    </ContextMenuGroup>
  </ContextMenuContent>
  }

  const clipboardContext=(item:ClipboardItem)=><ContextMenuContent finalFocus={false}>
    <ContextMenuGroup>
      <ContextMenuItem onClick={()=>void copyClipboardItem(item.id)}><CopyIcon/>复制</ContextMenuItem>
      <ContextMenuItem onClick={()=>void run(async()=>{await ClipboardService.TogglePinned(item.id);await reloadClipboard()})}>{item.pinned?<PinOffIcon/>:<PinIcon/>}{item.pinned?"取消置顶":"置顶"}</ContextMenuItem>
      <ContextMenuSeparator/>
      {item.text&&<ContextMenuItem onClick={()=>void paste(item.id,true)}><FileTextIcon/>以纯文本粘贴</ContextMenuItem>}
      {(item.text||(item.files??[]).length>0)&&<ContextMenuItem onClick={()=>void addClipboardToPhrase(item)}><PlusIcon/>添加到快捷短语</ContextMenuItem>}
      <ContextMenuItem onClick={()=>void run(async()=>{await ClipboardService.Delete(item.id);setUndoVisible(true);window.setTimeout(()=>setUndoVisible(false),8000);await reloadClipboard()})}><Trash2Icon/>删除</ContextMenuItem>
    </ContextMenuGroup>
  </ContextMenuContent>

  const tree=(parent="",depth=0):React.ReactNode=>childNodes(nodes,parent).map(node=>{
      const open=expanded.has(node.id)
      if(node.type==="folder"){
        const childCount=childNodes(nodes,node.id).length
        return <li key={node.id}>
        <ContextMenu onOpenChange={open=>setContextId(open?node.id:null)}>
          <ContextMenuTrigger className={contextId===node.id?"rounded bg-accent":undefined}>
            <button {...dragProps(node)} className={`flex h-7 w-full min-w-0 items-center gap-1 rounded pr-2 text-left text-xs hover:bg-accent ${dropTarget===node.id?"bg-accent ring-2 ring-inset ring-ring":""}`} style={{paddingLeft:4+depth*12}} aria-expanded={open} onClick={()=>setExpanded(current=>{const next=new Set(current);if(open)next.delete(node.id);else next.add(node.id);return next})}>
              {open?<FolderOpenIcon className="size-3.5 shrink-0"/>:<FolderIcon className="size-3.5 shrink-0"/>}<span className="min-w-0 truncate">{node.name}</span>
              {node.completionEnabled&&<span className="rounded bg-primary/10 px-1 text-[9px] text-primary">补全</span>}
              {childCount>0&&<span aria-hidden="true" title={`${childCount} 个子项`} className="ml-auto shrink-0 text-xs tabular-nums text-muted-foreground">{childCount}</span>}
            </button>
          </ContextMenuTrigger>
          {phraseContext(node)}
        </ContextMenu>
        {open&&<ul>{tree(node.id,depth+1)}</ul>}
      </li>
      }
      return <li key={node.id}>
       <ContextMenu onOpenChange={open=>setContextId(open?node.id:null)}>
        <ContextMenuTrigger className={contextId===node.id?"rounded bg-accent":undefined}>
         <button {...dragProps(node)} data-selectable="true" disabled={busy} onClick={()=>void insert(node.id)} style={{paddingLeft:4+depth*12}} className="flex min-h-7 w-full min-w-0 items-center gap-1 rounded py-0.5 text-xs hover:bg-accent focus:bg-accent disabled:opacity-50">
           <FileTextIcon className="size-3.5 shrink-0 text-muted-foreground"/><span className="min-w-0 text-left"><span className="block truncate leading-3.5">{node.name}</span>{node.content&&<span className="block truncate text-[11px] leading-3 text-muted-foreground">{node.content.replace(/\s+/g," ")}</span>}</span>
         </button>
        </ContextMenuTrigger>
        {phraseContext(node)}
       </ContextMenu>
      </li>
    })

  const destinations=[{value:"",label:"根目录"},...nodes.filter(n=>n.type==="folder").map(folder=>({value:folder.id,label:phrasePath(folder,nodes)==="根目录"?folder.name:`${phrasePath(folder,nodes)} / ${folder.name}`}))]
  const editorDestinations=editor?.id?destinations.filter(folder=>canMove(nodes,editor.id!,folder.value)):destinations

  const editorView=editor&&<Dialog open onOpenChange={open=>{if(!open)closeEditor()}}>
    <DialogContent className="max-h-[calc(100svh-1rem)] overflow-y-auto" finalFocus={false}>
      <DialogTitle>{editor.id?editor.kind==="folder"?"重命名文件夹":"编辑短语":editor.kind==="folder"?"新建文件夹":"新建短语"}</DialogTitle>
      <DialogDescription className="sr-only">设置所属文件夹、名称{editor.kind==="phrase"?"和内容":""}</DialogDescription>
      <form className="flex flex-col gap-4" onSubmit={event=>{event.preventDefault();void saveEditor()}}>
        <FieldGroup className="gap-3">
          <Field><FieldLabel htmlFor="node-folder">所属文件夹</FieldLabel>
            <Select items={editorDestinations} value={editor.parentId} onValueChange={value=>{if(value!==null)setEditor({...editor,parentId:value})}}>
              <SelectTrigger id="node-folder" className="w-full"><SelectValue/></SelectTrigger>
              <SelectContent alignItemWithTrigger={false}><SelectGroup>{editorDestinations.map(folder=><SelectItem key={folder.value} value={folder.value}>{folder.label}</SelectItem>)}</SelectGroup></SelectContent>
            </Select>
          </Field>
          <Field><FieldLabel htmlFor="node-name">名称</FieldLabel><Input id="node-name" autoFocus value={editor.name} onChange={e=>setEditor({...editor,name:e.target.value})}/></Field>
          {editor.kind==="phrase"&&<Field><FieldLabel htmlFor="phrase-content">内容</FieldLabel><Textarea id="phrase-content" rows={3} value={editor.content} onChange={e=>setEditor({...editor,content:e.target.value})}/></Field>}
        </FieldGroup>
        <div className="flex justify-end gap-2"><Button type="button" variant="outline" onClick={closeEditor}>取消</Button><Button type="submit" disabled={busy||!editor.name.trim()}>保存</Button></div>
      </form>
    </DialogContent>
  </Dialog>

  const moveView=<Dialog open={moveNode!==null} onOpenChange={open=>{if(!open&&!busy)setMoveNode(null)}}>
    <DialogContent className="flex h-[440px] max-h-[calc(100svh-1rem)] flex-col" finalFocus={false}>
      <DialogTitle className="break-all pr-8">移动“{moveNode?.name}”</DialogTitle>
      <DialogDescription className="sr-only">选择目标文件夹</DialogDescription>
      <ScrollArea className="min-h-0 flex-1 rounded-md border"><div role="tree" aria-label="目标文件夹" className="p-1"><div role="treeitem" aria-expanded="true" aria-selected={moveTarget===""}>
        <button type="button" className={`flex h-7 w-full items-center gap-2 rounded-md px-2 text-sm ${moveTarget===""?"bg-accent":"hover:bg-accent"}`} onClick={()=>setMoveTarget("")}><FolderOpenIcon className="size-4"/>根目录</button>
        {moveNode&&<PhraseDestinationTree nodes={nodes} source={moveNode.id} selected={moveTarget} onSelect={setMoveTarget}/>}
      </div></div></ScrollArea>
      <div className="flex shrink-0 justify-end gap-2"><Button variant="outline" disabled={busy} onClick={()=>setMoveNode(null)}>取消</Button><Button disabled={busy||!moveNode||moveTarget===(moveNode.parentId??"")||!canMove(nodes,moveNode.id,moveTarget)} onClick={()=>void submitMove()}>移动</Button></div>
    </DialogContent>
  </Dialog>

  return <div className="relative flex h-svh select-none flex-col overflow-hidden border bg-popover text-popover-foreground" onKeyDown={event=>{
    if(editor||event.nativeEvent.isComposing||event.defaultPrevented||(event.target as HTMLElement).closest('[role="menu"]'))return
    if(event.key==="ArrowDown"||event.key==="ArrowUp"){
      const choices=Array.from(event.currentTarget.querySelectorAll<HTMLButtonElement>('button[data-selectable="true"]'))
      if(choices.length){event.preventDefault();const current=choices.indexOf(document.activeElement as HTMLButtonElement);const delta=event.key==="ArrowDown"?1:-1;choices[current<0?(delta>0?0:choices.length-1):(current+delta+choices.length)%choices.length].focus()}
      return
    }
    if(event.key==="Enter"&&event.shiftKey){const active=document.activeElement as HTMLElement;const id=active?.dataset.clipboardId;if(id&&active.dataset.plain==="true"){event.preventDefault();void paste(id,true);return}}
    if(event.key==="Escape"){if(editor)closeEditor();else void BrowserService.Hide()}
  }}>
    <header onPointerDown={event=>{if(event.button===0&&!(event.target as HTMLElement).closest("button,input,[data-no-drag]")){event.preventDefault();void BrowserService.BeginDrag().catch(e=>setError(String(e)))}}} className="relative grid h-9 shrink-0 cursor-move grid-cols-[1fr_auto_1fr] items-center px-1">
      <div aria-hidden="true"/>
      <div data-no-drag className="flex h-full cursor-default items-center gap-1">
        <button onClick={()=>setTab("clipboard")} className={`rounded-md px-3 py-1 text-xs ${tab==="clipboard"?"bg-accent font-medium":"hover:bg-accent/60"}`}>剪贴板</button>
        <button onClick={()=>setTab("phrase")} className={`rounded-md px-3 py-1 text-xs ${tab==="phrase"?"bg-accent font-medium":"hover:bg-accent/60"}`}>短语</button>
      </div>
      <div className="flex justify-end"><button className={`flex size-7 items-center justify-center rounded-md hover:bg-accent ${pinned?"bg-accent text-foreground":""}`} aria-label={pinned?"取消固定":"固定快捷面板"} onClick={()=>void BrowserService.SetPinned(!pinned).then(()=>setPinned(!pinned)).catch(e=>setError(String(e)))}>{pinned?<PinOffIcon className="size-4"/>:<PinIcon className="size-4"/>}</button><button className="flex size-7 items-center justify-center rounded-md hover:bg-accent" aria-label="打开主界面" onClick={()=>void BrowserService.OpenManager()}><AppWindowIcon className="size-4"/></button><button className="flex size-7 items-center justify-center rounded-md hover:bg-accent" aria-label="关闭快捷面板" onClick={()=>void BrowserService.Hide()}><XIcon className="size-4"/></button></div>
      <span aria-hidden="true" className="pointer-events-none absolute inset-x-0 bottom-0 border-b"/>
    </header>
    <div className="flex h-8 shrink-0 items-center gap-1 border-b px-2 py-1">
      <SearchIcon className="size-3.5 text-muted-foreground"/><Input ref={searchRef} className="h-6 border-0 px-1 text-xs shadow-none focus-visible:ring-0" placeholder={tab==="clipboard"?"搜索剪贴板":"搜索名称、内容或路径"} value={queries[tab]} onChange={e=>setQueries({...queries,[tab]:e.target.value})}/>
    </div>
    {tab==="clipboard"&&!captureEnabled&&<button className="shrink-0 border-b bg-muted/50 px-3 py-2 text-left text-xs text-muted-foreground hover:text-foreground" onClick={()=>void run(async()=>{await ClipboardService.SetEnabled(true);setCaptureEnabled(true)})}>剪贴板已关闭，点击开启</button>}
    <ScrollArea key={transientVersion} className="min-h-0 flex-1" viewportClassName="[&>[data-slot=scroll-area-content]]:min-h-full [&>[data-slot=scroll-area-content]]:flex [&>[data-slot=scroll-area-content]]:flex-col" onScrollCapture={()=>{clearDetailTimer();void BrowserService.DismissDetail()}}>
      {loading?<div className="min-h-full flex-1"/>:tab==="phrase"?<ContextMenu><ContextMenuTrigger className={`flex flex-1 flex-col ${dropTarget===""?"bg-accent/40 ring-2 ring-inset ring-ring":""}`} onDragOver={event=>{if(!draggedId||!canMove(nodes,draggedId,""))return;event.preventDefault();event.dataTransfer.dropEffect="move";setDropTarget("")}} onDragLeave={event=>{if(!event.currentTarget.contains(event.relatedTarget as Node|null))setDropTarget(current=>current===""?null:current)}} onDrop={event=>{event.preventDefault();const id=event.dataTransfer.getData("application/x-quick-paste-node")||draggedId||"";setDraggedId(null);setDropTarget(null);if(canMove(nodes,id,""))void run(()=>movePhraseNode(id,""))}}><ul className="p-0.5">{phraseResults?phraseResults.map(({node,path})=><li key={node.id}>
        <ContextMenu onOpenChange={open=>setContextId(open?node.id:null)}>
          <ContextMenuTrigger className={contextId===node.id?"rounded bg-accent":undefined}>
            <button {...dragProps(node)} data-selectable="true" onClick={()=>void insert(node.id)} className="flex min-h-7 w-full min-w-0 items-center gap-1 rounded px-1 py-0.5 text-left hover:bg-accent focus:bg-accent"><FileTextIcon className="size-3.5 shrink-0 text-muted-foreground"/><span className="min-w-0"><span className="block truncate text-xs leading-3.5">{node.name}</span><span className="block truncate text-[11px] leading-3 text-muted-foreground">{path}{node.content?` · ${node.content.replace(/\s+/g," ")}`:""}</span></span></button>
          </ContextMenuTrigger>
          {phraseContext(node)}
        </ContextMenu>
      </li>):tree()}{(phraseResults?.length===0||(!phraseResults&&nodes.length===0))&&<p className="p-5 text-center text-xs text-muted-foreground">{queries.phrase?"没有匹配的短语":"暂无短语"}</p>}</ul></ContextMenuTrigger><ContextMenuContent finalFocus={false}><ContextMenuGroup><ContextMenuItem onClick={()=>openEditor(undefined,"","phrase")}><PlusIcon/>新建短语</ContextMenuItem><ContextMenuItem onClick={()=>openEditor(undefined,"","folder")}><FolderPlusIcon/>新建文件夹</ContextMenuItem></ContextMenuGroup></ContextMenuContent></ContextMenu>:
      <div role="listbox" className="flex flex-col gap-1.5 p-1.5">{items.map(item=><ContextMenu key={item.id} onOpenChange={open=>setContextId(open?item.id:null)}>
        <ContextMenuTrigger>
          <div className={`group relative rounded-md border hover:border-border has-[:focus-visible]:bg-accent ${contextId===item.id?"border-border bg-accent":"bg-background/40 hover:bg-accent"} ${item.kind==="image"?"h-32":"h-16"}`} onPointerEnter={event=>{clearDetailTimer();const element=event.currentTarget;const summary=element.querySelector<HTMLElement>("[data-clipboard-summary]");const truncated=!!summary&&(summary.scrollHeight>summary.clientHeight||summary.scrollWidth>summary.clientWidth);if(item.kind!=="image"&&!truncated)return;detailTimer.current=setTimeout(()=>{detailTimer.current=null;if(element.isConnected&&element.matches(":hover")){const bounds=element.getBoundingClientRect();void BrowserService.ShowDetail(item.id,Math.round(bounds.top),Math.round(bounds.height))}},tooltipDelay)}} onPointerLeave={()=>{clearDetailTimer();void BrowserService.HideDetail(item.id)}} onPointerDown={()=>{clearDetailTimer();void BrowserService.DismissDetail()}}>
            <button data-selectable="true" data-clipboard-id={item.id} data-plain={item.text?"true":"false"} onClick={()=>void paste(item.id)} onAuxClick={e=>{if(e.button===1&&item.text){e.preventDefault();void paste(item.id,true)}}} className="grid h-full w-full grid-rows-[16px_minmax(0,1fr)] gap-1 px-2 py-1 text-left focus-visible:bg-accent">
              <span className="flex min-w-0 items-center gap-1 overflow-hidden pr-20 text-[10px] leading-4 text-muted-foreground">{item.sourceIcon?<img src={item.sourceIcon} alt="" className="size-3 shrink-0 rounded-sm"/>:item.kind==="image"?<ImageIcon className="size-3 shrink-0"/>:item.kind==="files"?<FileIcon className="size-3 shrink-0"/>:item.kind==="rich"?<FileTextIcon className="size-3 shrink-0"/>:<ClipboardIcon className="size-3 shrink-0"/>}<span className="max-w-25 truncate">{item.source||"未知来源"}</span><span>·</span><time className="shrink-0">{relativeTime(item.createdAt)}</time>{item.kind==="rich"&&<><span>·</span><span className="shrink-0">富文本</span></>}{item.kind==="files"&&<><span>·</span><span className="shrink-0">文件</span></>}{item.kind==="image"&&item.preview!=="图片"&&<><span>·</span><span className="shrink-0">{item.preview.replace(/^图片\s*/,"")}</span></>}{item.pinned&&<PinIcon className="size-2.5 shrink-0"/>}</span>
              <span data-clipboard-summary className="line-clamp-2 min-h-0 overflow-hidden break-all text-xs leading-4">{item.kind==="image"?<ClipboardThumbnail id={item.id}/>:item.preview}</span>
            </button>
            <span className="absolute right-1 top-0.5 hidden gap-0.5 group-hover:flex group-has-[:focus-visible]:flex">
              <Tooltip><TooltipTrigger render={<Button variant="ghost" size="icon-xs" className="size-5 text-muted-foreground hover:!bg-foreground/10 hover:text-foreground" aria-label={copiedId===item.id?"已复制":"复制"} onClick={()=>void copyClipboardItem(item.id)}/>}>{copiedId===item.id?<CheckIcon/>:<CopyIcon/>}</TooltipTrigger><TooltipContent>{copiedId===item.id?"已复制":"复制"}</TooltipContent></Tooltip>
              <Tooltip><TooltipTrigger render={<Button variant="ghost" size="icon-xs" className="size-5 text-muted-foreground hover:!bg-foreground/10 hover:text-foreground" aria-label={item.pinned?"取消置顶":"置顶"} onClick={()=>void run(async()=>{await ClipboardService.TogglePinned(item.id);await reloadClipboard()})}/>}>{item.pinned?<PinOffIcon/>:<PinIcon/>}</TooltipTrigger><TooltipContent>{item.pinned?"取消置顶":"置顶"}</TooltipContent></Tooltip>
              <Tooltip><TooltipTrigger render={<Button variant="ghost" size="icon-xs" className="size-5 text-muted-foreground hover:!bg-foreground/10 hover:text-foreground" aria-label="删除" onClick={()=>void run(async()=>{await ClipboardService.Delete(item.id);setUndoVisible(true);window.setTimeout(()=>setUndoVisible(false),8000);await reloadClipboard()})}/> }><Trash2Icon/></TooltipTrigger><TooltipContent>删除</TooltipContent></Tooltip>
            </span>
          </div>
        </ContextMenuTrigger>
        {clipboardContext(item)}
      </ContextMenu>)}{items.length===0&&<p className="p-5 text-center text-xs text-muted-foreground">{queries.clipboard?"没有匹配项":captureEnabled?"尚无剪贴板历史":"剪贴板记录已暂停"}</p>}</div>}
    </ScrollArea>
    {undoVisible&&<div className="flex shrink-0 items-center justify-between border-t bg-muted px-3 py-1.5 text-xs"><span>已删除剪贴板项目</span><button className="font-medium text-primary hover:underline" onClick={()=>void run(async()=>{await ClipboardService.UndoDelete();setUndoVisible(false);await reloadClipboard()})}>撤销</button></div>}
    {error&&<p role="alert" className="shrink-0 border-t px-2 py-1.5 text-xs text-destructive">{error}</p>}
    {tab==="phrase"&&editorView}
    {tab==="phrase"&&moveView}
  </div>
}
