import { useEffect, useRef, useState } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { useTheme } from 'next-themes'
import { Button } from '@/components/ui/button'
import { Ellipsis, Settings2, Sparkles } from 'lucide-react'
import { InlineErrorNotice } from '@/components/common/inline-error-notice'
import { isPluginPageRoute, type PluginPageRoute, type WorkspaceMode } from '@/stores/workspace-store'
import { GamePlayer, type GamePlayerHandle } from './GamePlayer'
import { PluginPageAgent } from './PluginPageAgent'
import type { PagePresentation } from './page-presentation'
import { RuntimeSetup } from './RuntimeSetup'
import {
  localized,
  management,
  platformError,
  releasePluginConsumer,
  type PluginPage,
  type ProjectPlugin,
  type ProjectExtensionConfiguration,
  type RuntimeSnapshot,
} from './api'

export function usePluginPages() {
  return useQuery({
    queryKey: ['platform', 'plugin-pages'],
    queryFn: () => management<PluginPage[]>('/plugin-pages'),
    refetchInterval: 5000,
  })
}

/** Navigation selects a destination; its consumer and Agent remain bound to the
 * same Project while the user visits other routes. Project changes revoke it. */
export function PluginPageWorkspace({
  projectId,
  route,
  visible,
  onChooseProject,
}: {
  projectId?: string
  route: WorkspaceMode
  visible: boolean
  onChooseProject: () => void
}) {
  const previous = useRef<PluginPageRoute | undefined>(undefined)
  if (isPluginPageRoute(route)) previous.current = route
  const identity = previous.current?.slice('plugin:'.length)
  const pages = usePluginPages()
  const page = pages.data?.find((page) => `${page.pluginId}/${page.id}` === identity)
  const { t } = useTranslation()
  if (!projectId)
    return (
      <div className="flex h-full flex-col items-center justify-center gap-3 p-6">
        <p>{t('platform.selectProject')}</p>
        <Button onClick={onChooseProject}>{t('platform.project')}</Button>
      </div>
    )
  if (!page) return <div className="p-6">{pages.isPending ? t('common.loading') : t('platform.pages.unavailable')}</div>
  return (
    <ProjectPluginPage
      key={`${projectId}/${identity}/${page.releaseId}`}
      page={page}
      projectId={projectId}
      visible={visible}
    />
  )
}

function ProjectPluginPage({ page, projectId, visible }: { page: PluginPage; projectId: string; visible: boolean }) {
  const { t, i18n } = useTranslation()
  const { resolvedTheme } = useTheme()
  const client = useQueryClient()
  const configurationKey = ['platform', 'project-config', projectId]
  const configuration = useQuery({
    queryKey: configurationKey,
    queryFn: () => management<ProjectExtensionConfiguration>(`/projects/${projectId}/extensions`),
  })
  const plugins = useQuery({
    queryKey: ['platform', 'project-plugins', projectId, 'general', i18n.language],
    queryFn: () =>
      management<ProjectPlugin[]>(
        `/projects/${projectId}/plugins?context=general&locale=${i18n.language.startsWith('zh') ? 'zh-CN' : 'en-US'}`,
      ),
  })
  const plugin = plugins.data?.find((item) => item.id === page.pluginId)
  const [models, setModels] = useState<Record<string, string>>({})
  const [runtime, setRuntime] = useState<RuntimeSnapshot>()
  const [error, setError] = useState('')
  const [saving, setSaving] = useState(false)
  const [configure, setConfigure] = useState(false)
  const [agentBusy, setAgentBusy] = useState(false)
  const [activation, setActivation] = useState(0)
  const [pageContext, setPageContext] = useState<unknown>(null)
  const [presentation, setPresentation] = useState<PagePresentation>({})
  const [showAssistant, setShowAssistant] = useState(false)
  const frame = useRef<GamePlayerHandle>(null)
  useEffect(() => {
    setModels(configuration.data?.extensions.models ?? {})
  }, [configuration.data])
  const selectedModels = configuration.data?.extensions.models ?? {}
  const binding = JSON.stringify(plugin?.models.map((slot) => [slot.key, selectedModels[slot.key]]) ?? [])
  const ready =
    !!plugin?.enabled &&
    !plugin.problem &&
    !!configuration.data &&
    plugin.models.every((slot) => !slot.required || !!selectedModels[slot.key])
  useEffect(() => {
    if (!ready) return
    let disposed = false
    let opened: RuntimeSnapshot | undefined
    setError('')
    void management<RuntimeSnapshot>('/plugin-actions/open', 'POST', {
      projectId,
      pluginId: page.pluginId,
      releaseId: page.releaseId,
      context: 'general',
      kind: 'page',
      actionId: page.id,
      consumerId: crypto.randomUUID(),
      locale: i18n.language,
      theme: resolvedTheme,
    })
      .then(async (value) => {
        opened = value
        if (disposed) {
          await releasePluginConsumer(value)
          return
        }
        setRuntime(value)
      })
      .catch((error) => {
        if (!disposed) setError(platformError(error))
      })
    return () => {
      disposed = true
      setRuntime(undefined)
      if (opened)
        void releasePluginConsumer(opened).catch((error) => console.error('[plugins] release page failed', error))
    }
    // Appearance updates use the existing frame protocol, without restarting work.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [ready, activation, binding, page.id, page.pluginId, page.releaseId, projectId])
  const save = async () => {
    if (!configuration.data) return
    if (agentBusy || frame.current?.isBusy()) {
      setError(t('platform.pages.busySettings'))
      return
    }
    setSaving(true)
    setError('')
    try {
      const saved = await management<ProjectExtensionConfiguration>(`/projects/${projectId}/extensions`, 'PUT', {
        expectedRevision: configuration.data.revision,
        extensions: { ...configuration.data.extensions, models },
      })
      client.setQueryData(configurationKey, saved)
      setConfigure(false)
      if (!runtime) setActivation((value) => value + 1)
    } catch (error) {
      setError(platformError(error))
    } finally {
      setSaving(false)
    }
  }
  const title = localized(page.title, i18n.language)
  return (
    <section className="flex h-full min-h-0 flex-col" aria-label={title}>
      <header className="flex h-14 shrink-0 min-w-0 items-center justify-between gap-3 border-b px-5">
        <h1 className="truncate text-base font-medium" title={presentation.title || title}>
          {presentation.title || title}
        </h1>
        <div className="flex shrink-0 items-center gap-1 text-muted-foreground">
          <span className="mr-3 hidden text-xs sm:inline">{title}</span>
          {page.agent && runtime && (
            <Button
              className="lg:hidden"
              size="sm"
              variant={showAssistant ? 'secondary' : 'ghost'}
              aria-label={t('platform.pages.agent')}
              aria-pressed={showAssistant}
              onClick={() => setShowAssistant((value) => !value)}
            >
              <Sparkles data-icon="inline-start" />
              {t('platform.pages.assistantTab')}
            </Button>
          )}
          <Button
            size="icon-sm"
            variant="ghost"
            aria-label={t('platform.pages.models')}
            onClick={() => setConfigure((value) => !value)}
          >
            <Settings2 />
          </Button>
          {runtime && (
            <Button
              size="icon-sm"
              variant="ghost"
              aria-label={t('platform.plugins.panelMenu')}
              onClick={() => frame.current?.openMenu()}
            >
              <Ellipsis />
            </Button>
          )}
        </div>
      </header>
      {(error || configuration.error || plugins.error) && (
        <InlineErrorNotice className="m-3" message={error || platformError(configuration.error || plugins.error)} />
      )}
      {configure || !ready ? (
        <div className="mx-auto w-full max-w-xl overflow-y-auto p-6">
          {!plugin?.enabled && plugins.isSuccess && <p>{t('platform.plugins.disabled')}</p>}
          {plugin?.problem && <InlineErrorNotice message={t(plugin.problem.messageKey)} />}
          <p className="mb-4 text-sm text-muted-foreground">{t('platform.pages.setup')}</p>
          {plugin && (
            <RuntimeSetup
              projectLocked
              value={{ projectId, models, configuration: {} }}
              modelRequirements={plugin.models}
              onChange={(value) => setModels(value.models)}
            />
          )}
          <Button
            className="mt-4"
            disabled={
              saving ||
              !plugin?.enabled ||
              !configuration.data ||
              plugin.models.some((slot) => slot.required && !models[slot.key])
            }
            onClick={() => void save()}
          >
            {t('platform.pages.open')}
          </Button>
        </div>
      ) : null}
      {runtime && ready ? (
        <div className={configure ? 'hidden' : 'flex min-h-0 flex-1 flex-col lg:flex-row'}>
          <div className={`${showAssistant ? 'hidden lg:flex' : 'flex'} min-h-0 min-w-0 flex-1 flex-col`}>
            <GamePlayer
              ref={frame}
              runtime={runtime}
              title={title}
              visible={visible && !configure}
              variant="plugin"
              showMenu={false}
              externalBusy={agentBusy}
              onContext={setPageContext}
              onPresentation={setPresentation}
              onStop={() => releasePluginConsumer(runtime)}
              onExit={() => {
                setConfigure(true)
                setRuntime(undefined)
              }}
            />
          </div>
          {page.agent && (
            <aside
              className={`${showAssistant ? 'flex' : 'hidden lg:flex'} min-h-0 flex-1 flex-col lg:flex-none lg:w-[clamp(19rem,27vw,27rem)] lg:border-l`}
            >
              <PluginPageAgent
                runtime={runtime}
                page={page}
                context={pageContext}
                presentation={presentation}
                onPageAction={(id) => {
                  setShowAssistant(false)
                  frame.current?.pageAction(id)
                }}
                modelBinding={binding}
                onBusyChange={setAgentBusy}
                onChange={() => frame.current?.refresh()}
              />
            </aside>
          )}
        </div>
      ) : ready && !configure ? (
        <p className="p-6" role="status">
          {t('common.loading')}
        </p>
      ) : null}
    </section>
  )
}
