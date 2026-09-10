import { useState } from "react"
import { FileTextIcon, FolderIcon, FolderOpenIcon } from "lucide-react"
import { canMove, childNodes, type PhraseTreeNode } from "./phrase-tree"

type DestinationNode = PhraseTreeNode & { name: string }

export function PhraseDestinationTree<T extends DestinationNode>({ nodes, source, selected, onSelect, parentId = "" }: {
  nodes: T[]
  source: string
  selected: string
  onSelect: (id: string) => void
  parentId?: string
}) {
  return <ul role="group" className="ml-4">
    {childNodes(nodes,parentId).map(node=><PhraseDestinationNode key={node.id} {...{node,nodes,source,selected,onSelect}}/>)}
  </ul>
}

function PhraseDestinationNode<T extends DestinationNode>({node,nodes,source,selected,onSelect}: {
  node:T
  nodes:T[]
  source:string
  selected:string
  onSelect:(id:string)=>void
}) {
  const [open,setOpen]=useState(false)
  const childCount=node.type==="folder"?childNodes(nodes,node.id).length:0
  const disabled=node.type!=="folder"||!canMove(nodes,source,node.id)
  return <li role="treeitem" aria-selected={selected===node.id} aria-disabled={disabled} aria-expanded={node.type==="folder"?open:undefined}>
    <button type="button" disabled={disabled} onClick={()=>{onSelect(node.id);setOpen(!open)}} className={`flex h-7 w-full items-center gap-2 rounded-md px-2 text-left text-sm disabled:cursor-not-allowed disabled:opacity-40 hover:enabled:bg-accent ${selected===node.id?"bg-accent":""}`}>
      {node.type==="phrase"?<FileTextIcon className="size-4"/>:open?<FolderOpenIcon className="size-4"/>:<FolderIcon className="size-4"/>}
      <span className="min-w-0 truncate">{node.name}</span>
      {childCount>0&&<span aria-hidden="true" title={`${childCount} 个子项`} className="ml-auto shrink-0 text-xs tabular-nums text-muted-foreground">{childCount}</span>}
    </button>
    {node.type==="folder"&&open&&<PhraseDestinationTree {...{nodes,source,selected,onSelect}} parentId={node.id}/>}
  </li>
}
