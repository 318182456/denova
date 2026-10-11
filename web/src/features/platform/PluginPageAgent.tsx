import { useCallback, useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { APIError } from '@/lib/api-client'
import { ArrowUp, ImageIcon, Loader2, Paperclip, Sparkles, Square } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Textarea } from '@/components/ui/textarea'
import { InputGroup, InputGroupAddon, InputGroupButton, InputGroupTextarea } from '@/components/ui/input-group'
import { Badge } from '@/components/ui/badge'
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover'
import type { PagePresentation } from './page-presentation'
import { InlineErrorNotice } from '@/components/common/inline-error-notice'
import { MarkdownContent } from '@/components/Chat/message-content'
import { consumer, platformError, type PluginPage, type RuntimeSnapshot } from './api'

interface HistoryItem {
  recordId: string
  role: string
  text: string
}
interface History {
  items: HistoryItem[]
  cursor: string
}
interface Question {
  id: string
  prompt: string
  options?: { value: string; label: string }[]
  multiple?: boolean
}
interface Interaction {
  id: string
  kind: string
  questions?: Question[]
}
interface Run {
  run: { runId: string }
  status: string
  text: string
  error?: { messageKey: string }
  interactions?: Interaction[]
}
const running = (run?: Run) => !!run && ['accepted', 'running', 'waiting'].includes(run.status)

/** Uses the platform's canonical Product Session and execution lifecycle. The
 * frame supplies bounded user context; it cannot select credentials or tools. */
export function PluginPageAgent({
  runtime,
  page,
  context,
  presentation,
  onPageAction,
  modelBinding,
  onChange,
  onBusyChange,
}: {
  runtime: RuntimeSnapshot
  page: PluginPage
  context: unknown
  presentation: PagePresentation
  onPageAction: (id: string) => void
  modelBinding: string
  onChange: () => void
  onBusyChange: (busy: boolean) => void
}) {
  const { t } = useTranslation()
  const [sessionId, setSessionId] = useState('')
  const [history, setHistory] = useState<History>({ items: [], cursor: '' })
  const [run, setRun] = useState<Run>()
  const [draft, setDraft] = useState('')
  const [error, setError] = useState('')
  const [pending, setPending] = useState(false)
  const [command, setCommand] = useState('')
  const composer = useRef<HTMLTextAreaElement>(null)
  const contextRef = useRef(context)
  contextRef.current = context
  const scroll = useRef<HTMLDivElement>(null)
  const changeRef = useRef(onChange)
  changeRef.current = onChange
  useEffect(() => {
    onBusyChange(pending || !!command)
    return () => onBusyChange(false)
  }, [pending, command, onBusyChange])
  const storageKey = `denova.plugin-page.command.${sessionId}`
  const loadHistory = useCallback(
    async (id: string, signal?: AbortSignal) => {
      setHistory(await consumer<History>(runtime, `/agents/sessions/${id}/history?limit=100`, 'GET', undefined, signal))
    },
    [runtime],
  )
  useEffect(() => {
    const abort = new AbortController()
    void (async () => {
      const digest = await crypto.subtle.digest('SHA-256', new TextEncoder().encode(modelBinding))
      const modelKey = Array.from(new Uint8Array(digest), (byte) => byte.toString(16).padStart(2, '0')).join('')
      const session = await consumer<{ ref: { sessionId: string } }>(
        runtime,
        '/agents/sessions',
        'POST',
        {
          projectId: runtime.context.scope.projectId,
          definition: `page:${page.id}`,
          key: `${page.id}/${page.releaseId}/${modelKey}`,
        },
        abort.signal,
      )
      if (abort.signal.aborted) return
      setSessionId(session.ref.sessionId)
      await loadHistory(session.ref.sessionId, abort.signal)
      const saved = localStorage.getItem(`denova.plugin-page.command.${session.ref.sessionId}`)
      if (saved) setCommand(JSON.parse(saved).commandId)
    })().catch((error) => {
      if (!abort.signal.aborted) setError(platformError(error))
    })
    return () => abort.abort()
  }, [runtime, page.id, page.releaseId, modelBinding, loadHistory])
  useEffect(() => {
    if (!sessionId || !command) return
    const abort = new AbortController()
    let timer: ReturnType<typeof setTimeout>
    const poll = async () => {
      try {
        let value: Run
        try {
          value = await consumer<Run>(
            runtime,
            `/agents/sessions/${sessionId}/runs?commandId=${encodeURIComponent(command)}`,
            'GET',
            undefined,
            abort.signal,
          )
        } catch (error) {
          if (!(error instanceof APIError) || error.code !== 'NOT_FOUND') throw error
          const saved = localStorage.getItem(storageKey)
          if (!saved) throw error
          // A lost admission response is retried with the exact saved request.
          // The host receipt fences the command and prevents another paid run.
          value = await consumer<Run>(
            runtime,
            `/agents/sessions/${sessionId}/runs`,
            'POST',
            JSON.parse(saved),
            abort.signal,
          )
        }
        if (abort.signal.aborted) return
        setRun(value)
        if (running(value)) {
          timer = setTimeout(() => void poll(), 700)
          return
        }
        await loadHistory(sessionId, abort.signal)
        if (abort.signal.aborted) return
        localStorage.removeItem(storageKey)
        setCommand('')
        setRun(undefined)
        if (value.error) setError(t(value.error.messageKey))
        else if (value.status !== 'completed') setError(t('platform.pages.runIncomplete'))
        changeRef.current()
      } catch (error) {
        if (!abort.signal.aborted) {
          setError(platformError(error))
          if (error instanceof APIError && error.status >= 400 && error.status < 500) {
            localStorage.removeItem(storageKey)
            setCommand('')
            setRun(undefined)
          } else timer = setTimeout(() => void poll(), 3000)
        }
      }
    }
    void poll()
    return () => {
      abort.abort()
      clearTimeout(timer)
    }
  }, [runtime, sessionId, command, storageKey, loadHistory, t])
  useEffect(() => {
    scroll.current?.scrollTo({ top: scroll.current.scrollHeight, behavior: 'smooth' })
  }, [history.items.length, run?.text])
  const send = async () => {
    if (!draft.trim() || !sessionId || pending || command) return
    setPending(true)
    setError('')
    const text = draft
    const commandId = crypto.randomUUID()
    // Keep the transport command before admission, so an uncertain response is
    // recovered with the same ID instead of submitting another model request.
    const input = { commandId, input: { text, context: contextRef.current } }
    localStorage.setItem(storageKey, JSON.stringify(input))
    try {
      const value = await consumer<Run>(runtime, `/agents/sessions/${sessionId}/runs`, 'POST', input)
      setRun(value)
      setDraft('')
      setCommand(commandId)
      await loadHistory(sessionId)
    } catch (error) {
      setError(platformError(error))
      if (error instanceof APIError && error.status >= 400 && error.status < 500) localStorage.removeItem(storageKey)
      else setCommand(commandId)
    } finally {
      setPending(false)
    }
  }
  const cancel = async () => {
    if (!run) return
    try {
      await consumer(runtime, `/agents/runs/${run.run.runId}/cancel`, 'POST', {})
      setError('')
    } catch (error) {
      setError(platformError(error))
    }
  }
  return (
    <div className="flex min-h-0 flex-1 flex-col" aria-label={t('platform.pages.agent')}>
      <header className="flex h-14 shrink-0 items-center justify-between gap-2 px-5">
        <span className="flex items-center gap-2 font-medium">
          <Sparkles className="size-4" />
          {t('platform.pages.agent')}
        </span>
        <Popover>
          <PopoverTrigger asChild>
            <Button size="sm" variant="ghost" className="font-normal text-muted-foreground">
              {t('platform.pages.context')}
            </Button>
          </PopoverTrigger>
          <PopoverContent align="end" className="flex max-w-[90vw] flex-col gap-2 text-sm">
            <p className="font-medium">{presentation.contextLabel || t('platform.pages.context')}</p>
            <p className="leading-relaxed text-muted-foreground">{t('platform.pages.contextHint')}</p>
            {!!presentation.referenceCount && (
              <p className="text-xs text-muted-foreground">
                {t('platform.pages.referenceCount', { count: presentation.referenceCount })}
              </p>
            )}
          </PopoverContent>
        </Popover>
      </header>
      {presentation.contextLabel && (
        <div
          className="mx-4 mb-1 flex shrink-0 items-center gap-2 rounded-lg bg-muted/60 px-3 py-2.5 text-xs text-muted-foreground"
          title={presentation.contextLabel}
        >
          <ImageIcon className="size-4 shrink-0" />
          <span className="truncate">{presentation.contextLabel}</span>
        </div>
      )}
      <div
        ref={scroll}
        className="flex min-h-0 flex-1 flex-col gap-5 overflow-y-auto px-5 py-5 text-sm leading-relaxed"
      >
        {history.cursor && (
          <Button
            variant="ghost"
            size="sm"
            onClick={() =>
              void consumer<History>(
                runtime,
                `/agents/sessions/${sessionId}/history?limit=100&cursor=${history.cursor}`,
              )
                .then((older) =>
                  setHistory((value) => ({ items: [...older.items, ...value.items], cursor: older.cursor })),
                )
                .catch((error) => setError(platformError(error)))
            }
          >
            {t('platform.pages.older')}
          </Button>
        )}
        {history.items.length === 0 && (
          <div className="py-2">
            <p className="mb-2 text-xs text-muted-foreground">{t('platform.pages.agent')}</p>
            <p className="text-muted-foreground">{t('platform.pages.agentHint')}</p>
          </div>
        )}
        {history.items
          .filter((item) => item.role === 'user' || item.role === 'assistant')
          .map((item) => (
            <div key={item.recordId} className={item.role === 'user' ? 'ml-5 rounded-xl bg-muted px-4 py-3' : 'py-1'}>
              {item.role === 'assistant' && (
                <p className="mb-2 text-xs text-muted-foreground">{t('platform.pages.agent')}</p>
              )}
              <MarkdownContent
                content={item.text}
                highlightDialogue={false}
                projectId={runtime.context.scope.projectId ?? ''}
              />
            </div>
          ))}
        {run?.text && (
          <MarkdownContent
            content={run.text}
            highlightDialogue={false}
            projectId={runtime.context.scope.projectId ?? ''}
            streaming={running(run)}
          />
        )}
        {command && (
          <p role="status" className="flex items-center gap-2 text-xs text-muted-foreground">
            <Loader2 className="size-3 animate-spin" />
            {t(run?.status === 'waiting' ? 'platform.pages.waiting' : 'platform.pages.working')}
          </p>
        )}
        {run?.interactions
          ?.filter((interaction) => interaction.kind === 'ask')
          .map((interaction) => (
            <PageAgentQuestion
              key={interaction.id}
              interaction={interaction}
              onAnswer={(answer) =>
                consumer(
                  runtime,
                  `/agents/runs/${run.run.runId}/interactions/${interaction.id}/responses`,
                  'POST',
                  answer,
                )
              }
            />
          ))}
        {error && <InlineErrorNotice message={error} />}
      </div>
      <form
        className="flex shrink-0 flex-col gap-3 p-4 pt-1"
        onSubmit={(event) => {
          event.preventDefault()
          void send()
        }}
      >
        {!!presentation.suggestions?.length && (
          <div className="hidden flex-wrap gap-2 lg:flex">
            {presentation.suggestions.map((item) => (
              <Button
                key={item.label}
                type="button"
                size="sm"
                variant="secondary"
                className="font-normal"
                onClick={() => {
                  setDraft(item.prompt)
                  composer.current?.focus()
                }}
              >
                {item.label}
              </Button>
            ))}
          </div>
        )}
        <InputGroup className="rounded-xl bg-transparent! has-disabled:opacity-100">
          <InputGroupTextarea
            ref={composer}
            aria-label={t('platform.pages.prompt')}
            value={draft}
            onChange={(event) => setDraft(event.target.value)}
            placeholder={t('platform.pages.composerHint')}
            className="min-h-20 max-h-44 px-4 py-4 lg:min-h-32"
            onKeyDown={(event) => {
              if (event.key === 'Enter' && (event.metaKey || event.ctrlKey)) {
                event.preventDefault()
                void send()
              }
            }}
          />
          <InputGroupAddon align="block-end" className="justify-between px-3 pb-3">
            <div className="flex min-w-0 items-center gap-1">
              {presentation.actions?.map((action) => (
                <InputGroupButton key={action.id} type="button" size="sm" onClick={() => onPageAction(action.id)}>
                  <Paperclip data-icon="inline-start" />
                  {action.label}
                  {!!presentation.referenceCount && <Badge variant="secondary">{presentation.referenceCount}</Badge>}
                </InputGroupButton>
              ))}
            </div>
            {command ? (
              <InputGroupButton
                type="button"
                size="icon"
                variant="secondary"
                aria-label={t('common.cancel')}
                onClick={() => void cancel()}
                disabled={!run}
              >
                <Square />
              </InputGroupButton>
            ) : (
              <InputGroupButton
                type="submit"
                size="icon"
                variant="default"
                aria-label={t('platform.pages.send')}
                disabled={!sessionId || pending || !draft.trim()}
              >
                <ArrowUp />
              </InputGroupButton>
            )}
          </InputGroupAddon>
        </InputGroup>
      </form>
    </div>
  )
}

function PageAgentQuestion({
  interaction,
  onAnswer,
}: {
  interaction: Interaction
  onAnswer: (answer: unknown) => Promise<unknown>
}) {
  const { t } = useTranslation()
  const [answers, setAnswers] = useState<Record<string, { values: string[]; text: string }>>({})
  const [pending, setPending] = useState(false)
  const [error, setError] = useState('')
  const submit = async () => {
    setPending(true)
    setError('')
    try {
      await onAnswer({
        answers: (interaction.questions ?? []).map((question) => ({
          question_id: question.id,
          values: answers[question.id]?.text.trim() ? [] : (answers[question.id]?.values ?? []),
          text: answers[question.id]?.text ?? '',
        })),
      })
    } catch (error) {
      setError(platformError(error))
      setPending(false)
    }
  }
  return (
    <fieldset disabled={pending} className="space-y-3 rounded-lg border p-3">
      {interaction.questions?.map((question) => (
        <div key={question.id} className="space-y-2">
          <label htmlFor={question.id}>{question.prompt}</label>
          {question.options?.map((option) => (
            <label key={option.value} className="flex items-center gap-2">
              <input
                type={question.multiple ? 'checkbox' : 'radio'}
                name={question.id}
                checked={answers[question.id]?.values.includes(option.value) ?? false}
                onChange={(event) =>
                  setAnswers((value) => ({
                    ...value,
                    [question.id]: {
                      text: '',
                      values: question.multiple
                        ? event.target.checked
                          ? [...(value[question.id]?.values ?? []), option.value]
                          : (value[question.id]?.values ?? []).filter((item) => item !== option.value)
                        : [option.value],
                    },
                  }))
                }
              />
              {option.label}
            </label>
          ))}
          <Textarea
            id={question.id}
            aria-label={question.prompt}
            placeholder={t('platform.pages.answer')}
            value={answers[question.id]?.text ?? ''}
            onChange={(event) =>
              setAnswers((value) => ({ ...value, [question.id]: { values: [], text: event.target.value } }))
            }
          />
        </div>
      ))}
      {error && <InlineErrorNotice message={error} />}
      <Button size="sm" onClick={() => void submit()}>
        {t('platform.pages.send')}
      </Button>
    </fieldset>
  )
}
