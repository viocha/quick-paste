import type { ComponentProps } from "react"
import { Dialog as Primitive } from "@base-ui/react/dialog"
import { XIcon } from "lucide-react"
import { cn } from "cn"
import { Button } from "./button"

export const Dialog = Primitive.Root
export const DialogTitle = Primitive.Title
export const DialogDescription = Primitive.Description
export function DialogContent({ className, children, ...props }: ComponentProps<typeof Primitive.Popup>) {
 return <Primitive.Portal>
  <Primitive.Backdrop className="fixed inset-0 z-50 bg-black/20 data-open:animate-in data-open:fade-in-0 data-closed:animate-out data-closed:fade-out-0 duration-100" />
  <Primitive.Popup className={cn("fixed left-1/2 top-1/2 z-50 grid w-[calc(100%-2rem)] max-w-md -translate-x-1/2 -translate-y-1/2 gap-4 rounded-xl border bg-popover p-5 text-popover-foreground shadow-lg outline-none data-open:animate-in data-open:fade-in-0 data-closed:animate-out data-closed:fade-out-0 duration-100",className)} {...props}>
   {children}
   <Primitive.Close render={<Button variant="ghost" size="icon-sm" className="absolute right-3 top-3" aria-label="关闭" />}><XIcon /></Primitive.Close>
  </Primitive.Popup>
 </Primitive.Portal>
}
