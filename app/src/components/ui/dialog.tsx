import * as React from "react";
import * as D from "@radix-ui/react-dialog";
import { X } from "lucide-react";
import { cn } from "@/lib/utils";

export const Dialog = D.Root;
export const DialogTrigger = D.Trigger;
export const DialogClose = D.Close;

export const DialogContent = React.forwardRef<
  HTMLDivElement,
  React.ComponentPropsWithoutRef<typeof D.Content> & { wide?: boolean; hideClose?: boolean }
>(({ className, children, wide, hideClose, ...p }, ref) => (
  <D.Portal>
    <D.Overlay className="fixed inset-0 z-50 bg-black/50 data-[state=open]:animate-in data-[state=closed]:animate-out data-[state=closed]:fade-out-0 data-[state=open]:fade-in-0" />
    <D.Content
      ref={ref}
      className={cn(
        "fixed left-1/2 top-1/2 z-50 grid max-h-[90vh] w-[calc(100%-2rem)] -translate-x-1/2 -translate-y-1/2 grid-cols-[minmax(0,1fr)] gap-4 overflow-y-auto rounded-lg border bg-background p-6 shadow-lg",
        wide ? "max-w-3xl" : "max-w-lg",
        className,
      )}
      {...p}
    >
      {children}
      {!hideClose && (
        <D.Close className="absolute right-4 top-4 rounded-sm opacity-70 hover:opacity-100" aria-label="close">
          <X className="size-4" />
        </D.Close>
      )}
    </D.Content>
  </D.Portal>
));
DialogContent.displayName = "DialogContent";

export const DialogHeader = ({ className, ...p }: React.HTMLAttributes<HTMLDivElement>) => (
  <div className={cn("flex flex-col space-y-1.5", className)} {...p} />
);
export const DialogFooter = ({ className, ...p }: React.HTMLAttributes<HTMLDivElement>) => (
  <div className={cn("flex flex-row flex-wrap justify-end gap-2", className)} {...p} />
);
export const DialogTitle = React.forwardRef<HTMLHeadingElement, React.ComponentPropsWithoutRef<typeof D.Title>>(
  ({ className, ...p }, ref) => <D.Title ref={ref} className={cn("text-lg font-semibold", className)} {...p} />,
);
DialogTitle.displayName = "DialogTitle";
export const DialogDescription = React.forwardRef<HTMLParagraphElement, React.ComponentPropsWithoutRef<typeof D.Description>>(
  ({ className, ...p }, ref) => <D.Description ref={ref} className={cn("break-words text-sm text-muted-foreground [overflow-wrap:anywhere]", className)} {...p} />,
);
DialogDescription.displayName = "DialogDescription";
