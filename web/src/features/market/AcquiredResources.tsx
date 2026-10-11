import { useEffect, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import {
  ArchiveRestore,
  ChevronDown,
  Download,
  FileArchive,
  Link2,
  Package,
  RefreshCw,
  Settings2,
  Unlink,
  Upload,
} from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Card,
  CardContent,
  CardDescription,
  CardFooter,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'
import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from '@/components/ui/collapsible'
import {
  Empty,
  EmptyDescription,
  EmptyHeader,
  EmptyMedia,
  EmptyTitle,
} from '@/components/ui/empty'
import { Field, FieldGroup, FieldLabel } from '@/components/ui/field'
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Separator } from '@/components/ui/separator'
import { cn } from '@/lib/utils'
import { getBooks } from '@/lib/api'
import { localized, type CatalogEntry } from '@/features/platform/api'
import { openInstalledExtension } from '@/features/platform/extension-navigation'
import { InstalledExtensionToggle } from '@/features/platform/InstalledExtensionToggle'
import { exchange, type Installation, type Preview } from './api'
import { BackupDialog } from './BackupDialog'
import type { ImportDialogProps } from './ImportDialog'
import { GameDefaultsDialog } from '@/features/interactive/components/GameDefaultsDialog'

export function AcquiredResources({
  installations,
  onChanged,
  onImport,
  onExport,
  onDiscover,
  requested,
  extensions,
  blockedReason,
}: {
  installations: Installation[]
  onChanged: () => Promise<void>
  onImport: (props: Omit<ImportDialogProps, 'onClose' | 'onInstalled'>) => void
  onExport: (props: { installation?: Installation }) => void
  onDiscover: () => void
  requested?: string
  extensions: CatalogEntry[]
  blockedReason?: string
}) {
  const { t, i18n } = useTranslation()
  const books = useQuery({ queryKey: ['resources', 'books'], queryFn: getBooks, enabled: installations.some(item => !!item.project_id) })
  const [expanded, setExpanded] = useState<string>()
  const [backup, setBackup] = useState<Installation>()
  const [defaults, setDefaults] = useState<Installation>()
  const [pending, setPending] = useState('')
  useEffect(() => {
    if (requested) setExpanded(requested)
  }, [requested])
  const run = async (id: string, action: () => Promise<void>) => {
    setPending(id)
    try {
      await action()
    } catch (error) {
      console.error('[market] installed package action failed', {
        installationID: id,
        error,
      })
      toast.error(
        error instanceof Error
          ? error.message
          : t('market.errors.operationFailed'),
      )
    } finally {
      setPending('')
    }
  }
  return (
    <>
      {installations.length ? (
        <div className="grid min-w-0 grid-cols-[repeat(auto-fill,minmax(min(100%,18rem),1fr))] items-start gap-3">
          {installations.map((item) => {
            const canCheckUpdate =
              item.tracking === 'tracked' && item.source.kind !== 'file'
            const translation = item.package.translations?.[i18n.language]
            const name = translation?.name || item.package.name
            const description = translation?.description || item.package.description
            const kinds = [...new Set(item.bindings.map(binding => binding.local.kind))]
            const targetName = item.project_id
              ? books.data?.find(book => book.project_id === item.project_id)?.name || item.project_id
              : t('market.import.global')
            return (
              <Collapsible
                key={item.installation_id}
                open={expanded === item.installation_id}
                onOpenChange={(open) =>
                  setExpanded(open ? item.installation_id : undefined)
                }
                asChild
              >
                <Card
                  size="sm"
                  data-testid="market-installation-card"
                  className={cn('@container/installation min-w-0 gap-0 py-0 transition-shadow hover:ring-foreground/25', expanded === item.installation_id && 'col-span-full')}
                >
                  <CardHeader className="relative min-w-0 gap-2 p-3 transition-colors hover:bg-muted/40 has-focus-visible:ring-2 has-focus-visible:ring-inset has-focus-visible:ring-ring">
                      <CardTitle className="flex min-w-0 items-center gap-2">
                        <Package className="size-4 shrink-0 text-muted-foreground" />
                        <CollapsibleTrigger title={name} className={cn('min-w-0 flex-1 text-left after:absolute after:inset-0 focus:outline-none', expanded === item.installation_id ? '[overflow-wrap:anywhere]' : 'truncate')}>
                          {name}
                        </CollapsibleTrigger>
                        <ChevronDown aria-hidden className={cn('size-4 shrink-0 text-muted-foreground transition-transform', expanded === item.installation_id && 'rotate-180')} />
                      </CardTitle>
                      <CardDescription className={cn('break-words', expanded !== item.installation_id && 'line-clamp-2 h-10')}>{description || kinds.map(kind => t(`market.kinds.${kind}`)).join(' · ')}</CardDescription>
                      <div className="flex min-w-0 flex-wrap gap-1.5">
                        <Badge variant="secondary">
                          {t(
                            `market.states.${item.tracking === 'detached' ? 'detached' : item.local_state || 'unchanged'}`,
                          )}
                        </Badge>
                        {item.remote_state === 'update_available' &&
                          item.tracking === 'tracked' && (
                            <Badge>
                              <RefreshCw data-icon="inline-start" />
                              {t('market.updateAvailable')}
                            </Badge>
                          )}
                        {item.remote_state &&
                          ['check_failed', 'identity_changed'].includes(item.remote_state) && (
                            <Badge variant="outline">
                              {t(`market.states.${item.remote_state}`)}
                            </Badge>
                          )}
                      </div>
                  </CardHeader>
                  <CardContent className="flex min-w-0 flex-wrap gap-1.5 px-3 pb-3">
                    {kinds.slice(0, 3).map(kind => <Badge key={kind} variant="outline">{t(`market.kinds.${kind}`)}</Badge>)}
                    {kinds.length > 3 && <Badge variant="outline" title={kinds.slice(3).map(kind => t(`market.kinds.${kind}`)).join(' · ')}>+{kinds.length - 3}</Badge>}
                  </CardContent>
                  <CardFooter className="flex-wrap justify-between gap-x-3 gap-y-1 px-3 py-2 text-xs text-muted-foreground">
                    <span className="min-w-0 flex-1 truncate" title={targetName}>{targetName}</span>
                    <span>{t('market.acquired.resourceCount', { count: item.bindings.length })}</span>
                    {item.package.version && <span className="max-w-full truncate" title={item.package.version}>v{item.package.version}</span>}
                  </CardFooter>
                  <CollapsibleContent>
                    <Separator />
                    <CardContent className="grid min-w-0 gap-4 p-3 @2xl/installation:grid-cols-[minmax(0,1fr)_minmax(0,2fr)]">
                      <div className="flex min-w-0 flex-col gap-3 rounded-lg bg-muted/30 p-3">
                        <div className="flex min-w-0 flex-col gap-2">
                          <h3 className="flex items-center gap-2 text-sm font-medium">
                            {item.source.kind === 'file' ? (
                              <FileArchive className="size-4 text-muted-foreground" />
                            ) : (
                              <Link2 className="size-4 text-muted-foreground" />
                            )}
                            {t('market.acquired.source')}
                          </h3>
                          <p className="text-xs leading-relaxed text-muted-foreground [overflow-wrap:anywhere]">
                            {item.source.url || item.source.filename}
                          </p>
                          {(item.source.ref || item.source.path) && <p className="text-xs text-muted-foreground [overflow-wrap:anywhere]">{[item.source.ref, item.source.path].filter(Boolean).join(' · ')}</p>}
                          {item.source.commit && <p className="text-xs text-muted-foreground [overflow-wrap:anywhere]">{t('market.import.commit', { commit: item.source.commit.slice(0, 12) })}</p>}
                        </div>
                        {canCheckUpdate && (
                          <FieldGroup>
                            <Field>
                              <FieldLabel htmlFor={`market-policy-${item.installation_id}`} className="text-xs">
                                {t('market.updatePolicy')}
                              </FieldLabel>
                              <Select
                                value={item.update_mode}
                                disabled={!!pending}
                                onValueChange={(mode) =>
                                  void run(item.installation_id, async () => {
                                    await exchange(
                                      `/installations/${item.installation_id}/policy`,
                                      { update_mode: mode },
                                    )
                                    await onChanged()
                                  })
                                }
                              >
                                <SelectTrigger
                                  id={`market-policy-${item.installation_id}`}
                                  className="w-full min-w-0 [&_[data-slot=select-value]]:truncate"
                                >
                                  <SelectValue />
                                </SelectTrigger>
                                <SelectContent>
                                  <SelectGroup>
                                    <SelectItem value="manual">
                                      {t('market.policy.manual')}
                                    </SelectItem>
                                    <SelectItem value="notify">
                                      {t('market.policy.notify')}
                                    </SelectItem>
                                    {item.bindings.every((binding) =>
                                      binding.local.kind === 'skill' ||
                                      binding.local.kind === 'style.reference' ||
                                      binding.local.kind.startsWith('preset.'),
                                    ) && (
                                      <SelectItem value="auto_apply">
                                        {t('market.policy.auto_apply')}
                                      </SelectItem>
                                    )}
                                  </SelectGroup>
                                </SelectContent>
                              </Select>
                            </Field>
                          </FieldGroup>
                        )}
                      </div>
                      <div className="flex min-w-0 flex-col gap-2">
                        <h3 className="text-sm font-medium">{t('market.contents.title')}</h3>
                        <ul className="grid min-w-0 grid-cols-1 gap-x-4 @4xl/installation:grid-cols-2">
                          {item.bindings.map((binding) => {
                            const extension = extensions.find(entry => !entry.removed && `extension.${entry.kind}` === binding.local.kind && entry.id === binding.local.id)
                            const manifest = extension?.releases.find(release => release.ref.releaseId === extension.currentRelease)?.manifest
                            return (
                            <li
                              key={binding.resource_id}
                              className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1 border-b py-2 last:border-0"
                            >
                              <Badge variant="secondary" className="max-w-full">
                                {t(`market.kinds.${binding.local.kind}`)}
                              </Badge>
                              <span
                                title={binding.local.id}
                                className="min-w-0 flex-1 truncate font-mono text-xs text-muted-foreground"
                              >
                                {localized(manifest?.name, i18n.language) || binding.local.id}
                              </span>
                              {extension && <div className="flex items-center gap-2">
                                <InstalledExtensionToggle item={extension} onChanged={onChanged} disabled={!!pending} />
                                <Button size="sm" variant="link" className="h-auto p-0" onClick={() => openInstalledExtension(extension.kind, extension.id)}>{t('market.manageExtension')}</Button>
                              </div>}
                              {(binding.upstream_removed || binding.ownership === 'reference') && (
                                <div className="flex basis-full flex-wrap gap-1.5">
                                  {binding.upstream_removed && (
                                    <Badge variant="outline" className="h-auto whitespace-normal">
                                      {t('market.states.upstream_removed')}
                                    </Badge>
                                  )}
                                  {binding.ownership === 'reference' && (
                                    <Badge variant="outline" className="h-auto whitespace-normal">
                                      {t('market.actions.reference')}
                                    </Badge>
                                  )}
                                </div>
                              )}
                            </li>
                          )})}
                        </ul>
                      </div>
                    </CardContent>
                    <CardFooter className="flex-wrap gap-2 rounded-t-none p-3">
                      {canCheckUpdate && (
                        <Button
                          size="sm"
                          disabled={!!pending || !!blockedReason}
                          onClick={() =>
                            void run(item.installation_id, async () => {
                              const preview = await exchange<Preview>(
                                `/installations/${item.installation_id}/check`,
                                {},
                              )
                              await onChanged()
                              onImport({
                                preview,
                                installation: item,
                              })
                            })
                          }
                        >
                          <RefreshCw data-icon="inline-start" />
                          {t('market.checkUpdate')}
                        </Button>
                      )}
                      {item.tracking === 'tracked' && <Button size="sm" variant="outline" disabled={!!pending || !!blockedReason} onClick={() => onImport({ installation: item })}>
                        <Upload data-icon="inline-start" />{t('market.update.fromFile')}
                      </Button>}
                      <Button
                        size="sm"
                        variant="outline"
                        disabled={!!pending}
                        onClick={() => onExport({ installation: item })}
                      >
                        <Upload data-icon="inline-start" />
                        {t('market.export.title')}
                      </Button>
                      <Button size="sm" variant="outline" disabled={!!pending || !!blockedReason} onClick={() => setBackup(item)}>
                        <ArchiveRestore data-icon="inline-start" />
                        {t('market.backups.title')}
                      </Button>
                      {item.game_defaults && Object.keys(item.game_defaults).length > 0 && (
                        <Button size="sm" variant="outline" disabled={!!pending} onClick={() => setDefaults(item)}>
                          <Settings2 data-icon="inline-start" />
                          {t('gameDefaults.adopt')}
                        </Button>
                      )}
                      {item.tracking === 'tracked' && (
                        <Button
                          size="sm"
                          variant="ghost"
                          disabled={!!pending}
                          onClick={() => void run(item.installation_id, async () => {
                            await exchange(`/installations/${item.installation_id}/detach`, {})
                            await onChanged()
                            toast.success(t('market.detached'))
                          })}
                        >
                          <Unlink data-icon="inline-start" />
                          {t('market.detach')}
                        </Button>
                      )}
                    </CardFooter>
                  </CollapsibleContent>
                </Card>
              </Collapsible>
            )
          })}
        </div>
      ) : (
        <Empty>
          <EmptyHeader>
            <EmptyMedia variant="icon">
              <Download />
            </EmptyMedia>
            <EmptyTitle>{t('market.acquiredEmpty')}</EmptyTitle>
            <EmptyDescription>{t('market.acquiredHelp')}</EmptyDescription>
          </EmptyHeader>
          <Button onClick={() => onDiscover()}>{t('market.discover')}</Button>
        </Empty>
      )}
      {backup && (
        <BackupDialog
          installation={backup}
          onClose={() => setBackup(undefined)}
          onChanged={onChanged}
        />
      )}
      {defaults?.game_defaults && <GameDefaultsDialog defaults={defaults.game_defaults} projectID={defaults.project_id} onClose={() => setDefaults(undefined)} />}
    </>
  )
}
