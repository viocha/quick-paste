import { useTooltipDelay, useTooltipTimeout } from "./TooltipDelay"
import { PhraseRow } from "./PhraseRow"
import { DetailApp } from "./DetailApp"
import { BrowserApp } from "./BrowserApp"
import { toast } from "@/components/ui/toast"
import { createContext, useContext, useEffect, useMemo, useRef, useState } from "react"
import type { CSSProperties, DragEvent } from "react"
import {
  CheckIcon,
  CircleHelpIcon,
  FolderIcon,
  FolderOpenIcon,
  PencilIcon,
  FolderPlusIcon,
  PlusIcon,
  Trash2Icon,
  SettingsIcon,
  FolderInputIcon,
} from "lucide-react"
import { Events } from "@wailsio/runtime"

import { PhraseService } from "../bindings/quick-paste"
import type { PhraseNode as BindingPhraseNode } from "../bindings/quick-paste"
import { Dialog, DialogContent, DialogTitle, DialogDescription } from "@/components/ui/dialog"
import { Button } from "@/components/ui/button"
import { ScrollArea } from "@/components/ui/scroll-area"
import { SettingsDialog } from "./SettingsDialog"
import { HelpDialog } from "./HelpDialog"
import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from "@/components/ui/collapsible"
import {
  ContextMenu,
  ContextMenuContent,
  ContextMenuGroup,
  ContextMenuItem,
  ContextMenuSeparator,
  ContextMenuTrigger,
} from "@/components/ui/context-menu"
import { Field, FieldGroup, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import {
  Sidebar,
  SidebarContent,
  SidebarGroup,
  SidebarGroupContent,
  SidebarHeader,
  SidebarMenu,
  SidebarMenuButton,
  SidebarMenuItem,
  SidebarMenuSub,
  SidebarProvider,
} from "@/components/ui/sidebar"
import { Textarea } from "@/components/ui/textarea"
import { TooltipProvider } from "@/components/ui/tooltip"
import { canMove, childNodes, hasEnabledCompletionAncestor } from "./phrase-tree"
import { PhraseDestinationTree } from "./PhraseDestinationTree"

type NodeType = "folder" | "phrase"

type PhraseNode = {
  id: string
  parentId?: string
  type: NodeType
  name: string
  content?: string
  order: number
  completionEnabled?: boolean
}

type Suggestion = {
  id: string
  name: string
  content: string
}

type SuggestionPayload = {
  query: string
  items: Suggestion[]
  active: number
}

type TreeNodeProps = {
  node: PhraseNode
  nodes: PhraseNode[]
  selectedId: string | null
  editingId: string | null
  onSelect: (node: PhraseNode) => void
  onCreate: (kind: NodeType, parentId: string) => Promise<void>
  onRenameStart: (node: PhraseNode) => void
  onRenameCommit: (node: PhraseNode, name: string) => Promise<void>
  onRenameCancel: () => void
  onDelete: (node: PhraseNode) => void
  onToggleCompletion: (node: PhraseNode) => Promise<void>
}

const TreeActions = createContext<{
  dragged: string | null
  setDragged: (id: string | null) => void
  move: (id: string, target: string) => Promise<void>
  openMove: (node: PhraseNode) => void
} | null>(null)

function TreeNode({
  node,
  nodes,
  selectedId,
  editingId,
  onSelect,
  onCreate,
  onRenameStart,
  onRenameCommit,
  onRenameCancel,
  onDelete,
  onToggleCompletion,
}: TreeNodeProps) {
  const actions = useContext(TreeActions)!
  const [dropOver, setDropOver] = useState(false)
  const dragProps = {
    draggable: true,
    onDragStart: (event: DragEvent<HTMLElement>) => {
      event.stopPropagation()
      event.dataTransfer.setData("application/x-quick-paste-node", node.id)
      event.dataTransfer.effectAllowed = "move"
      actions.setDragged(node.id)
    },
    onDragEnd: () => { actions.setDragged(null); setDropOver(false) },
    onDragOver: (event: DragEvent<HTMLElement>) => {
      event.stopPropagation()
      if (node.type === "folder" && actions.dragged && canMove(nodes, actions.dragged, node.id)) {
        event.preventDefault(); event.dataTransfer.dropEffect = "move"; setDropOver(true)
      }
    },
    onDragLeave: (event: DragEvent<HTMLElement>) => {
      if (!event.currentTarget.contains(event.relatedTarget as Node | null)) setDropOver(false)
    },
    onDrop: (event: DragEvent<HTMLElement>) => {
      event.preventDefault(); event.stopPropagation(); setDropOver(false)
      const id = event.dataTransfer.getData("application/x-quick-paste-node")
      actions.setDragged(null)
      if (node.type === "folder" && canMove(nodes, id, node.id)) {
        setOpen(true); void actions.move(id, node.id)
      }
    },
  }
  const children = useMemo(() => childNodes(nodes, node.id), [nodes, node.id])
  const childCount = children.filter(child => child.id !== "draft").length
  const completionInherited = node.type === "folder" && hasEnabledCompletionAncestor(nodes, node)
  const [renameValue, setRenameValue] = useState(node.name)
  const [open, setOpen] = useState(false)
  const [menuOpen, setMenuOpen] = useState(false)
  const cancelled = useRef(false)
  useEffect(() => {
    if (children.some((child) => child.id === editingId)) setOpen(true)
  }, [children, editingId])

  useEffect(() => { setRenameValue(node.name); cancelled.current = false }, [node.name, editingId])

  if (editingId === node.id) {
    return (
      <SidebarMenuItem className="z-10 py-1">
        <Input
          autoFocus
          value={renameValue}
          onChange={(event) => setRenameValue(event.target.value)}
          onBlur={() => { if (!cancelled.current) void onRenameCommit(node, renameValue) }}
          aria-label={node.type === "folder" ? "文件夹名称" : "短语名称"}
          onKeyDown={(event) => {
            event.stopPropagation()
            if (event.nativeEvent.isComposing) return
            if (event.key === "Enter") {
              event.preventDefault()
              event.currentTarget.blur()
            }
            if (event.key === "Escape") {
              event.preventDefault()
              cancelled.current = true
              onRenameCancel()
            }
          }}
          className="h-7"
        />
      </SidebarMenuItem>
    )
  }

  const menu = (
    <ContextMenuContent finalFocus={false}>
      <ContextMenuGroup>
        {node.type === "folder" && (
          <>
            <ContextMenuItem onClick={() => void onCreate("phrase", node.id)}>
              <PlusIcon />
              新建短语
            </ContextMenuItem>
            <ContextMenuItem onClick={() => void onCreate("folder", node.id)}>
              <FolderPlusIcon />
              新建文件夹
            </ContextMenuItem>
            <ContextMenuItem disabled={completionInherited} onClick={() => void onToggleCompletion(node)}>
              {node.completionEnabled || completionInherited ? <CheckIcon /> : <span className="size-4" />}
              {completionInherited ? "自动补全（已继承）" : "自动补全"}
            </ContextMenuItem>
            <ContextMenuSeparator />
          </>
        )}
        <ContextMenuItem onClick={() => onRenameStart(node)}><PencilIcon />重命名</ContextMenuItem>
        <ContextMenuItem onClick={() => actions.openMove(node)}><FolderInputIcon />移动到</ContextMenuItem>
        <ContextMenuItem onClick={() => onDelete(node)}>
          <Trash2Icon />
          删除
        </ContextMenuItem>
      </ContextMenuGroup>
    </ContextMenuContent>
  )

  if (node.type === "folder") {
    return (
      <SidebarMenuItem>
        <Collapsible open={open} onOpenChange={setOpen}>
          <ContextMenu onOpenChange={setMenuOpen}>
            <ContextMenuTrigger>
              <CollapsibleTrigger render={<SidebarMenuButton {...dragProps} tooltip={node.name} className={dropOver ? "bg-sidebar-accent ring-2 ring-inset ring-ring" : menuOpen ? "bg-sidebar-accent" : ""} />}>
                {open ? <FolderOpenIcon /> : <FolderIcon />}
                <span className="min-w-0 truncate">{node.name}</span>
                {node.completionEnabled && <span className="shrink-0 rounded bg-primary/10 px-1 text-[9px] text-primary">补全</span>}
                {childCount > 0 && <span aria-hidden="true" title={`${childCount} 个子项`} className="ml-auto shrink-0 text-xs tabular-nums text-muted-foreground">{childCount}</span>}
              </CollapsibleTrigger>
            </ContextMenuTrigger>
            {menu}
          </ContextMenu>
          {children.length > 0 && (
            <CollapsibleContent>
              <SidebarMenuSub>
                {children.map((child) => (
                  <TreeNode
                    key={child.id}
                    node={child}
                    nodes={nodes}
                    selectedId={selectedId}
                    editingId={editingId}
                    onSelect={onSelect}
                    onCreate={onCreate}
                    onRenameStart={onRenameStart}
                    onRenameCommit={onRenameCommit}
                    onRenameCancel={onRenameCancel}
                    onDelete={onDelete}
                    onToggleCompletion={onToggleCompletion}
                  />
                ))}
              </SidebarMenuSub>
            </CollapsibleContent>
          )}
        </Collapsible>
      </SidebarMenuItem>
    )
  }

  return (
    <SidebarMenuItem>
      <ContextMenu onOpenChange={setMenuOpen}>
        <ContextMenuTrigger>
          <PhraseRow name={node.name} content={node.content} render={
            <SidebarMenuButton {...dragProps}
              className={`${node.content ? "h-auto min-h-11 py-1.5" : ""} ${menuOpen ? "bg-sidebar-accent" : ""}`}
              isActive={selectedId === node.id} onClick={() => onSelect(node)} />
          } />
        </ContextMenuTrigger>
        {menu}
      </ContextMenu>
    </SidebarMenuItem>
  )
}

function ManagerApp() {
  const tooltipDelay=useTooltipDelay()
  const tooltipTimeout=useTooltipTimeout()
  const [nodes, setNodes] = useState<PhraseNode[]>([])
  const [selectedId, setSelectedId] = useState<string | null>(null)
  const [editingId, setEditingId] = useState<string | null>(null)
  const [draftNode, setDraftNode] = useState<PhraseNode | null>(null)
  const [settingsOpen,setSettingsOpen]=useState(false)
  const [helpOpen,setHelpOpen]=useState(false)
  const deletePending=useRef(false)
  const [dragged,setDragged]=useState<string|null>(null)
  const [rootDropOver,setRootDropOver]=useState(false)
  useEffect(()=>{if(!dragged)setRootDropOver(false)},[dragged])
  const [moveNode,setMoveNode]=useState<PhraseNode|null>(null)
  const [moveOpen,setMoveOpen]=useState(false)
  const [moveTarget,setMoveTarget]=useState("")
  const [moving,setMoving]=useState(false)
  const [draft, setDraft] = useState<{ id: string; name: string; content: string } | null>(null)
  const saveTimer = useRef<ReturnType<typeof setTimeout> | null>(null)

  const selected = useMemo(
    () => nodes.find((node) => node.id === selectedId && node.type === "phrase") ?? null,
    [nodes, selectedId]
  )

  // A newly selected phrase renders its own values immediately, before any effects.
  const draftName = draft?.id === selected?.id ? draft?.name ?? "" : selected?.name ?? ""
  const draftContent = draft?.id === selected?.id ? draft?.content ?? "" : selected?.content ?? ""
  const setDraftName = (name: string) => {
    if (!selected) return
    setDraft((current) => ({ id: selected.id, name,
      content: current?.id === selected.id ? current.content : selected.content ?? "" }))
  }
  const setDraftContent = (content: string) => {
    if (!selected) return
    setDraft((current) => ({ id: selected.id, content,
      name: current?.id === selected.id ? current.name : selected.name }))
  }

  const reload = async (preferredId?: string) => {
    const next = (await PhraseService.ListNodes()) as PhraseNode[]
    setNodes(next)
    const phraseId = preferredId ?? selectedId ?? null
    setSelectedId(next.some((node) => node.id === phraseId && node.type === "phrase") ? phraseId : null)
  }

  useEffect(() => {
    void reload()
    const cancel=Events.On("phrases-changed",()=>void reload())
    return ()=>cancel()
  }, [])


  useEffect(() => {
    if (!selected || !draftName.trim()) return
    if (draftName === selected.name && draftContent === (selected.content ?? "")) return
    if (saveTimer.current) clearTimeout(saveTimer.current)
    saveTimer.current = setTimeout(() => {
      void PhraseService.UpdatePhrase(selected.id, draftName, draftContent).then(() => {
        setNodes((current) =>
          current.map((node) =>
            node.id === selected.id ? { ...node, name: draftName.trim(), content: draftContent } : node
          )
        )
      })
    }, 1000)
    return () => {
      if (saveTimer.current) clearTimeout(saveTimer.current)
    }
  }, [draftName, draftContent, selected?.id, selected?.name, selected?.content])

  const createNode = async (kind: NodeType, parentId: string) => {
    setDraftNode({ id: "draft", parentId, type: kind, name: "", order: 0 })
    setEditingId("draft")
  }

  const cancelRename = () => {
    setEditingId(null)
    setDraftNode(null)
  }

  const renameNode = async (node: PhraseNode, name: string) => {
    const nextName = name.trim()
    if (node.id === "draft") {
      if (!nextName) { cancelRename(); return }
      const created = await PhraseService.CreateNode(node.parentId ?? "", node.type, nextName)
      cancelRename()
      await reload(node.type === "phrase" ? created.id : undefined)
      return
    }
    if (!nextName || nextName === node.name) {
      setEditingId(null)
      return
    }
    await PhraseService.RenameNode(node.id, nextName)
    setEditingId(null)
    if (node.id === selectedId) setDraftName(nextName)
    await reload(node.type === "phrase" ? node.id : undefined)
  }

  const deleteNodeNow = async (node: PhraseNode) => {
    if (deletePending.current) return
    const ids = new Set<string>([node.id])
    let changed = true
    while (changed) {
      changed = false
      for (const item of nodes) {
        if (ids.has(item.parentId ?? "") && !ids.has(item.id)) {
          ids.add(item.id)
          changed = true
        }
      }
    }
    const deleted = nodes.filter(item=>ids.has(item.id)).map(item=>({...item}))
    deletePending.current=true
    try {
      if (selectedId && ids.has(selectedId) && saveTimer.current) clearTimeout(saveTimer.current)
      await PhraseService.DeleteNode(node.id)
      setEditingId(null)
      if (selectedId && ids.has(selectedId)) { setSelectedId(null); setDraft(null) }
      await reload()
      const toastId=toast.add({
        type:"success",
        title:`已删除“${node.name}”`,
        timeout:8000,
        actionProps:{
          children:"撤回",
          onClick:()=>void (async()=>{
            try {
              await PhraseService.UndoDelete(deleted as unknown as BindingPhraseNode[])
              toast.close(toastId)
              await reload(node.type==="phrase" ? node.id : undefined)
            } catch(error) {
              toast.add({type:"error",title:"撤回失败",description:String(error)})
            }
          })(),
        },
      })
    } catch(error) {
      toast.add({type:"error",title:"操作失败",description:String(error)})
    } finally {
      deletePending.current=false
    }
  }
  const openMove = (node: PhraseNode) => { setMoveNode({...node});setMoveTarget(node.parentId ?? "");setMoveOpen(true) }
  const move = async (id:string,target:string) => {
    if (!canMove(nodes,id,target)) return
    try { await PhraseService.MoveNode(id,target); await reload(); }
    catch(error) {toast.add({type:"error",title:"操作失败",description:String(error)});throw error}
  }
  const toggleCompletion = async (node: PhraseNode) => {
    try {
      await PhraseService.SetFolderCompletion(node.id,!node.completionEnabled)
      await reload()
    } catch(error) {
      toast.add({type:"error",title:"操作失败",description:String(error)})
    }
  }
  const submitMove = async () => {
    if (!moveNode || moving) return
    setMoving(true)
    try {await move(moveNode.id,moveTarget);setMoveOpen(false)} catch {} finally {setMoving(false)}
  }
  const openSettings = async () => {
    try {
      if (selected && draftName.trim() && (draftName !== selected.name || draftContent !== (selected.content ?? ""))) {
        if(saveTimer.current) clearTimeout(saveTimer.current)
        await PhraseService.UpdatePhrase(selected.id,draftName,draftContent)
        await reload()
      }
      setSettingsOpen(true)
    } catch(error) { toast.add({type:"error",title:"操作失败",description:String(error)}) }
  }
  const restored = async () => {
    if(saveTimer.current) clearTimeout(saveTimer.current)
    setDraft(null);setDraftNode(null);setEditingId(null);setSelectedId(null)
    setNodes(await PhraseService.ListNodes() as PhraseNode[])
  }
  const treeNodes = draftNode ? [...nodes, draftNode] : nodes
  const roots = childNodes(treeNodes)

  return (
    <TooltipProvider delay={tooltipDelay} timeout={tooltipTimeout}>
      <TreeActions.Provider value={{dragged,setDragged,move:async(id,target)=>{try{await move(id,target)}catch{}},openMove}}>
      <SidebarProvider
        style={{ "--sidebar-width": "18rem" } as CSSProperties}
        className="h-svh min-h-0 overflow-hidden"
      >
        <Sidebar collapsible="none">
          <SidebarHeader className="flex h-13 flex-row items-center justify-between border-b px-4">
            <div className="truncate text-sm font-semibold">Quick Paste</div>
            <div className="flex items-center">
              <Button variant="ghost" size="icon-sm" aria-label="帮助" onClick={()=>setHelpOpen(true)}><CircleHelpIcon /></Button>
              <Button variant="ghost" size="icon-sm" aria-label="设置" onClick={()=>void openSettings()}><SettingsIcon /></Button>
            </div>
          </SidebarHeader>

          <ContextMenu>
            <ContextMenuTrigger className="flex min-h-0 flex-1">
              <SidebarContent className={rootDropOver && dragged ? "ring-2 ring-inset ring-ring bg-sidebar-accent/30" : ""}>
                <SidebarGroup className="p-2 pb-0">
                  <SidebarGroupContent>
                    <SidebarMenu>
                      {roots.map((node) => (
                        <TreeNode
                          key={node.id}
                          node={node}
                          nodes={treeNodes}
                          selectedId={selectedId}
                          editingId={editingId}
                          onSelect={(item) => setSelectedId(item.id)}
                          onCreate={createNode}
                          onRenameStart={(item) => setEditingId(item.id)}
                          onRenameCommit={renameNode}
                          onRenameCancel={cancelRename}
                          onDelete={node=>void deleteNodeNow(node)}
                          onToggleCompletion={toggleCompletion}
                        />
                      ))}
                    </SidebarMenu>
                  </SidebarGroupContent>
                </SidebarGroup>
                <div data-slot="root-drop-zone" aria-label="移动到根目录" className="h-7 shrink-0"
                  onDragOver={event=>{
                    if (!dragged || !canMove(nodes,dragged,"")) return
                    event.preventDefault();event.stopPropagation();event.dataTransfer.dropEffect="move";setRootDropOver(true)
                  }}
                  onDragLeave={()=>setRootDropOver(false)}
                  onDrop={event=>{
                    event.preventDefault();event.stopPropagation();setRootDropOver(false)
                    const id=event.dataTransfer.getData("application/x-quick-paste-node")
                    setDragged(null)
                    if (canMove(nodes,id,"")) void move(id,"").catch(()=>{})
                  }}
                />
              </SidebarContent>
            </ContextMenuTrigger>
            <ContextMenuContent finalFocus={false}>
              <ContextMenuGroup>
                <ContextMenuItem onClick={() => void createNode("phrase", "")}><PlusIcon />新建短语</ContextMenuItem>
                <ContextMenuItem onClick={() => void createNode("folder", "")}><FolderPlusIcon />新建文件夹</ContextMenuItem>
              </ContextMenuGroup>
            </ContextMenuContent>
          </ContextMenu>
        </Sidebar>

        <main className="flex min-w-0 flex-1 flex-col bg-background">
          <header className="flex h-13 items-center justify-between border-b px-6">
            <h1 className="text-sm font-medium">短语</h1>
          </header>

          {selected ? (
            <div className="flex min-h-0 flex-1 flex-col p-6">
              <FieldGroup className="h-full min-h-0 max-w-3xl">
                <Field data-invalid={!draftName.trim()}>
                  <FieldLabel htmlFor="phrase-name">名称</FieldLabel>
                  <Input
                    id="phrase-name"
                    value={draftName}
                    aria-invalid={!draftName.trim()}
                    onChange={(event) => setDraftName(event.target.value)}
                  />
                </Field>
                <Field className="min-h-0 flex-1">
                  <FieldLabel htmlFor="phrase-content">内容</FieldLabel>
                  <ScrollArea
                    className="min-h-0 flex-1 rounded-lg border border-input transition-colors focus-within:border-ring focus-within:ring-3 focus-within:ring-ring/50"
                    viewportClassName="[&>[data-slot=scroll-area-content]]:flex [&>[data-slot=scroll-area-content]]:min-h-full"
                  >
                    <Textarea
                      id="phrase-content"
                      value={draftContent}
                      onChange={(event) => setDraftContent(event.target.value)}
                      className="field-sizing-content min-h-full flex-1 resize-none overflow-hidden rounded-none border-0 focus-visible:ring-0"
                    />
                  </ScrollArea>
                </Field>
              </FieldGroup>
            </div>
          ) : (
            <div className="flex flex-1 items-center justify-center text-sm text-muted-foreground">
              选择一个短语
            </div>
          )}
        </main>

        <HelpDialog open={helpOpen} onOpenChange={setHelpOpen} />
        <SettingsDialog open={settingsOpen} onOpenChange={setSettingsOpen} onRestored={restored} />
        <Dialog open={moveOpen} onOpenChange={open=>{if(!moving)setMoveOpen(open)}} onOpenChangeComplete={open=>{if(!open)setMoveNode(null)}}>
          <DialogContent className="flex h-[560px] max-h-[calc(100svh-2rem)] flex-col">
            <DialogTitle className="break-all pr-8 text-base font-medium">移动“{moveNode?.name}”</DialogTitle>
            <DialogDescription className="sr-only">选择目标文件夹</DialogDescription>
            <ScrollArea className="min-h-0 flex-1 rounded-md border">
              <div role="tree" aria-label="目标文件夹" className="p-1">
                <div role="treeitem" aria-expanded="true" aria-selected={moveTarget===""}>
                  <button type="button" className={`flex h-7 w-full items-center gap-2 rounded-md px-2 text-sm ${moveTarget==="" ? "bg-accent" : "hover:bg-accent"}`} onClick={()=>setMoveTarget("")}><FolderOpenIcon className="size-4" />根目录</button>
                  {moveNode && <PhraseDestinationTree nodes={nodes} source={moveNode.id} selected={moveTarget} onSelect={setMoveTarget} />}
                </div>
              </div>
            </ScrollArea>
            <div className="flex shrink-0 justify-end gap-2">
              <Button variant="outline" disabled={moving} onClick={()=>setMoveOpen(false)}>取消</Button>
              <Button disabled={moving || !moveNode || moveTarget===(moveNode.parentId ?? "") || !canMove(nodes,moveNode.id,moveTarget)} onClick={()=>void submitMove()}>移动</Button>
            </div>
          </DialogContent>
        </Dialog>
      </SidebarProvider>
      </TreeActions.Provider>
    </TooltipProvider>
  )
}

function PopupApp() {
  const [payload, setPayload] = useState<SuggestionPayload>({ query: "", items: [], active: 0 })
  const listRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    const cancel = Events.On("suggestions", (event) => {
      setPayload(event.data as SuggestionPayload)
    })
    void Events.Emit("popup-ready")
    return () => cancel()
  }, [])

  useEffect(() => {
    listRef.current?.querySelector('[aria-selected="true"]')?.scrollIntoView({ block: "nearest" })
  }, [payload])

  return (
    <div className="flex h-svh select-none flex-col overflow-hidden bg-popover text-popover-foreground">
      <ScrollArea className="min-h-0 flex-1">
        <div ref={listRef} role="listbox" aria-label="补全候选" className="p-1">
          {payload.items.map((item, index) => (
            <div
              key={item.id}
              role="option"
              aria-selected={index === payload.active}
              className="flex h-7 items-center rounded-sm px-2 text-sm aria-selected:bg-accent aria-selected:text-accent-foreground"
            >
              <span className="truncate">
                <span className="font-medium">{item.name.slice(0, payload.query.length)}</span>
                <span className="text-muted-foreground">{item.name.slice(payload.query.length)}</span>
              </span>
            </div>
          ))}
        </div>
      </ScrollArea>
    </div>
  )
}

export default function App() {
  if (window.location.hash === "#detail") return <DetailApp />
  if (window.location.hash === "#browser") return <BrowserApp />
  const isPopup = window.location.hash === "#popup"
  return isPopup ? <PopupApp /> : <ManagerApp />
}

