import type { ReactNode } from "react";
import { cn } from "@/lib/utils";

export type ListPreviewLayoutMode = "stacked" | "split";

interface ListPreviewLayoutProps {
  layout: ListPreviewLayoutMode;
  children: ReactNode;
  testId?: string;
}

interface ListPreviewRegionProps {
  layout: ListPreviewLayoutMode;
  region: "list" | "preview";
  previewVisible: boolean;
  /** Reserve most of a stacked panel for an active editor; default stays Library's 45/55. */
  compactList?: boolean;
  children: ReactNode;
  surface?: "library-list" | "mail-list" | "mail-preview";
  testId?: string;
}

/** Shared Library/Mail list-preview frame. */
export function ListPreviewLayout({
  layout,
  children,
  testId,
}: ListPreviewLayoutProps) {
  return (
    <div
      className={cn(
        "flex min-h-0 flex-1",
        layout === "split" ? "flex-row" : "flex-col",
      )}
      data-layout={layout}
      {...(testId ? { "data-testid": testId } : {})}
    >
      {children}
    </div>
  );
}

export function ListPreviewRegion({
  layout,
  region,
  previewVisible,
  compactList = false,
  children,
  surface,
  testId,
}: ListPreviewRegionProps) {
  if (region === "list") {
    return (
      <div
        className={cn(
          "min-h-0 min-w-0",
          !previewVisible
            ? "flex-1"
            : layout === "split"
              ? "flex-[40]"
              : compactList ? "flex-[20]" : "flex-[45]",
          surface === "library-list" &&
            "overflow-y-auto p-[var(--space-2)] relative",
          surface === "mail-list" && "flex overflow-hidden",
        )}
        {...(testId ? { "data-testid": testId } : {})}
      >
        {children}
      </div>
    );
  }

  return (
    <div
      className={cn(
        "min-h-0 min-w-0 border-[var(--color-border)]",
        layout === "split" ? "flex-[60] border-l" : compactList ? "flex-[80] border-t" : "flex-[55] border-t",
        surface === "mail-preview" && "flex overflow-hidden",
      )}
      {...(testId ? { "data-testid": testId } : {})}
    >
      {children}
    </div>
  );
}
