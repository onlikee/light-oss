import { MarkGithubIcon } from "@primer/octicons-react";
import {
  preferenceToggleTriggerClassName,
  type PreferenceToggleSize,
} from "@/components/preference-toggle";
import { cn } from "@/lib/utils";

const repositoryUrl = "https://github.com/onlikee/light-oss";

export function GitHubLink({
  className,
  size = "sm",
}: {
  className?: string;
  size?: PreferenceToggleSize;
}) {
  return (
    <a
      aria-label="Open GitHub repository"
      className={cn(preferenceToggleTriggerClassName(size), className)}
      href={repositoryUrl}
      rel="noreferrer"
      target="_blank"
    >
      <MarkGithubIcon className="size-4 shrink-0" />
      <span className="leading-none">GitHub</span>
    </a>
  );
}
