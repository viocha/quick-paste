export type PhraseTreeNode = {
  id: string
  parentId?: string
  type: string
}

export function childNodes<T extends PhraseTreeNode>(nodes: T[], parentId = "") {
  return nodes
    .filter((node) => (node.parentId ?? "") === parentId)
    .sort((a, b) => {
      if (a.type !== b.type) return a.type === "folder" ? -1 : 1
      if (a.id === "draft") return -1
      if (b.id === "draft") return 1
      const left = "name" in a && typeof a.name === "string" ? a.name : ""
      const right = "name" in b && typeof b.name === "string" ? b.name : ""
      return left.localeCompare(right, "zh-CN", { numeric: true }) || a.id.localeCompare(b.id)
    })
}

export function hasEnabledCompletionAncestor<T extends PhraseTreeNode & { completionEnabled?: boolean }>(nodes: T[], node: T): boolean {
  const seen = new Set<string>()
  let parentId = node.parentId ?? ""
  while (parentId) {
    if (seen.has(parentId)) return false
    seen.add(parentId)
    const parent = nodes.find((candidate) => candidate.id === parentId)
    if (!parent || parent.type !== "folder") return false
    if (parent.completionEnabled) return true
    parentId = parent.parentId ?? ""
  }
  return false
}

export function canMove(nodes: PhraseTreeNode[], id: string, target: string): boolean {
  if (!nodes.some((node) => node.id === id)) return false
  const seen = new Set([id])
  while (target) {
    if (seen.has(target)) return false
    seen.add(target)
    const folder = nodes.find((node) => node.id === target)
    if (!folder || folder.type !== "folder") return false
    target = folder.parentId ?? ""
  }
  return true
}
