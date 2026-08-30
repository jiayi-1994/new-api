/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import { zodResolver } from '@hookform/resolvers/zod'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import axios from 'axios'
import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import * as z from 'zod'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'

import {
  resetModelRatios,
  updatePricingCommand,
  updateSystemOption,
} from '../api'
import { SettingsPageTitleStatusPortal } from '../components/settings-page-context'
import { SettingsSection } from '../components/settings-section'
import { useUpdateOption } from '../hooks/use-update-option'
import type { SystemOptionsResponse } from '../types'
import { GroupRatioForm } from './group-ratio-form'
import {
  buildPricingDocumentReplacement,
  PRICING_DOCUMENT_KEYS,
  type PricingDocumentKey,
} from './model-pricing-persistence'
import { ModelRatioForm } from './model-ratio-form'
import {
  ensureSystemOptionsCacheBase,
  tryAdoptCommittedPricingDocuments,
} from './pricing-document-cache'
import { ToolPriceSettings } from './tool-price-settings'
import { UpstreamRatioSync } from './upstream-ratio-sync'
import {
  formatJsonForTextarea,
  type JsonValidationError,
  normalizeJsonString,
  validateJsonString,
} from './utils'

type Translate = (key: string, options?: Record<string, unknown>) => string

function formatJsonValidationError(
  t: Translate,
  error?: JsonValidationError,
  fallback = 'Invalid JSON'
) {
  if (!error) return t(fallback)

  if (error.type === 'required') return t('Value is required')
  if (error.type === 'structure') {
    return t(
      fallback === 'Invalid JSON' ? 'JSON structure is invalid' : fallback
    )
  }

  let locationMessage: string
  if (error.line && error.column) {
    locationMessage = t(
      'JSON is invalid at line {{line}}, column {{column}}.',
      {
        line: error.line,
        column: error.column,
      }
    )
  } else if (error.position !== undefined) {
    locationMessage = t('JSON is invalid at position {{position}}.', {
      position: error.position,
    })
  } else {
    locationMessage = t('JSON is invalid. Please check the syntax.')
  }

  const parts = [locationMessage]

  if (error.missingCommaLine) {
    parts.push(
      t('Check line {{line}} for a missing comma.', {
        line: error.missingCommaLine,
      })
    )
  }

  return parts.join(' ')
}

function createJsonStringField(
  t: Translate,
  options?: Parameters<typeof validateJsonString>[1]
) {
  return z.string().superRefine((value, ctx) => {
    const result = validateJsonString(value, options)
    if (!result.valid) {
      ctx.addIssue({
        code: z.ZodIssueCode.custom,
        message: formatJsonValidationError(t, result.error, result.message),
      })
    }
  })
}

const createModelSchema = (t: Translate) =>
  z.object({
    ModelPrice: createJsonStringField(t),
    ModelRatio: createJsonStringField(t),
    CacheRatio: createJsonStringField(t),
    CreateCacheRatio: createJsonStringField(t),
    CompletionRatio: createJsonStringField(t),
    ImageRatio: createJsonStringField(t),
    AudioRatio: createJsonStringField(t),
    AudioCompletionRatio: createJsonStringField(t),
    ExposeRatioEnabled: z.boolean(),
    BillingMode: createJsonStringField(t),
    BillingExpr: createJsonStringField(t),
    TaskBillingMode: createJsonStringField(t),
    VideoResolutionPrice: createJsonStringField(t),
  })

const createGroupSchema = (t: Translate) =>
  z.object({
    GroupRatio: createJsonStringField(t),
    TopupGroupRatio: createJsonStringField(t),
    UserUsableGroups: createJsonStringField(t),
    GroupGroupRatio: createJsonStringField(t),
    AutoGroups: createJsonStringField(t, {
      predicate: (parsed) =>
        Array.isArray(parsed) &&
        parsed.every((item) => typeof item === 'string'),
      predicateMessage: 'Expected a JSON array of group identifiers',
    }),
    DefaultUseAutoGroup: z.boolean(),
    GroupSpecialUsableGroup: createJsonStringField(t),
  })

type ModelFormValues = z.infer<ReturnType<typeof createModelSchema>>
type GroupFormValues = z.infer<ReturnType<typeof createGroupSchema>>
type RatioTabId =
  | 'models'
  | 'unset-models'
  | 'groups'
  | 'tool-prices'
  | 'upstream-sync'

type RatioSettingsCardProps = {
  modelDefaults: ModelFormValues
  groupDefaults: GroupFormValues
  toolPricesDefault: string
  titleKey?: string
  visibleTabs?: RatioTabId[]
}

function modelPricingDocuments(
  values: ModelFormValues
): Record<PricingDocumentKey, string> {
  return {
    ModelPrice: values.ModelPrice,
    ModelRatio: values.ModelRatio,
    CacheRatio: values.CacheRatio,
    CreateCacheRatio: values.CreateCacheRatio,
    CompletionRatio: values.CompletionRatio,
    ImageRatio: values.ImageRatio,
    AudioRatio: values.AudioRatio,
    AudioCompletionRatio: values.AudioCompletionRatio,
    'billing_setting.billing_mode': values.BillingMode,
    'billing_setting.billing_expr': values.BillingExpr,
    TaskBillingMode: values.TaskBillingMode,
    VideoResolutionPrice: values.VideoResolutionPrice,
  }
}

function modelFormValuesFromDocuments(
  documents: Record<PricingDocumentKey, string>,
  exposeRatioEnabled: boolean
): ModelFormValues {
  return {
    ModelPrice: documents.ModelPrice,
    ModelRatio: documents.ModelRatio,
    CacheRatio: documents.CacheRatio,
    CreateCacheRatio: documents.CreateCacheRatio,
    CompletionRatio: documents.CompletionRatio,
    ImageRatio: documents.ImageRatio,
    AudioRatio: documents.AudioRatio,
    AudioCompletionRatio: documents.AudioCompletionRatio,
    ExposeRatioEnabled: exposeRatioEnabled,
    BillingMode: documents['billing_setting.billing_mode'],
    BillingExpr: documents['billing_setting.billing_expr'],
    TaskBillingMode: documents.TaskBillingMode,
    VideoResolutionPrice: documents.VideoResolutionPrice,
  }
}

function normalizeModelFormValues(values: ModelFormValues): ModelFormValues {
  const documents = modelPricingDocuments(values)
  const normalizedDocuments = Object.fromEntries(
    Object.entries(documents).map(([key, value]) => [
      key,
      normalizeJsonString(value),
    ])
  ) as Record<PricingDocumentKey, string>
  return modelFormValuesFromDocuments(
    normalizedDocuments,
    values.ExposeRatioEnabled
  )
}

function formatModelFormValuesForEditing(
  values: ModelFormValues
): ModelFormValues {
  return {
    ...values,
    ModelPrice: formatJsonForTextarea(values.ModelPrice),
    ModelRatio: formatJsonForTextarea(values.ModelRatio),
    CacheRatio: formatJsonForTextarea(values.CacheRatio),
    CreateCacheRatio: formatJsonForTextarea(values.CreateCacheRatio),
    CompletionRatio: formatJsonForTextarea(values.CompletionRatio),
    ImageRatio: formatJsonForTextarea(values.ImageRatio),
    AudioRatio: formatJsonForTextarea(values.AudioRatio),
    AudioCompletionRatio: formatJsonForTextarea(values.AudioCompletionRatio),
    BillingMode: formatJsonForTextarea(values.BillingMode),
    BillingExpr: formatJsonForTextarea(values.BillingExpr),
    TaskBillingMode: formatJsonForTextarea(values.TaskBillingMode),
    VideoResolutionPrice: formatJsonForTextarea(values.VideoResolutionPrice),
  }
}

export function RatioSettingsCard({
  modelDefaults,
  groupDefaults,
  toolPricesDefault,
  titleKey = 'Pricing Ratios',
  visibleTabs = ['models', 'groups', 'tool-prices', 'upstream-sync'],
}: RatioSettingsCardProps) {
  const { t } = useTranslation()
  const updateOption = useUpdateOption()
  const queryClient = useQueryClient()
  const [confirmOpen, setConfirmOpen] = useState(false)
  const [modelSavePending, setModelSavePending] = useState(false)
  const [modelEditorRevision, setModelEditorRevision] = useState(0)
  const modelSavePendingRef = useRef(false)

  const resetMutation = useMutation({
    mutationFn: resetModelRatios,
    onSuccess: (data) => {
      if (data.success) {
        toast.success(t('Model prices reset successfully'))
        queryClient.invalidateQueries({ queryKey: ['system-options'] })
        setConfirmOpen(false)
      } else {
        toast.error(data.message || t('Failed to reset model ratios'))
      }
    },
    onError: (error: Error) => {
      toast.error(error.message || t('Failed to reset model ratios'))
    },
  })

  const pricingMutation = useMutation({ mutationFn: updatePricingCommand })

  const modelNormalizedDefaults = useRef({
    ModelPrice: normalizeJsonString(modelDefaults.ModelPrice),
    ModelRatio: normalizeJsonString(modelDefaults.ModelRatio),
    CacheRatio: normalizeJsonString(modelDefaults.CacheRatio),
    CreateCacheRatio: normalizeJsonString(modelDefaults.CreateCacheRatio),
    CompletionRatio: normalizeJsonString(modelDefaults.CompletionRatio),
    ImageRatio: normalizeJsonString(modelDefaults.ImageRatio),
    AudioRatio: normalizeJsonString(modelDefaults.AudioRatio),
    AudioCompletionRatio: normalizeJsonString(
      modelDefaults.AudioCompletionRatio
    ),
    ExposeRatioEnabled: modelDefaults.ExposeRatioEnabled,
    BillingMode: normalizeJsonString(modelDefaults.BillingMode),
    BillingExpr: normalizeJsonString(modelDefaults.BillingExpr),
    TaskBillingMode: normalizeJsonString(modelDefaults.TaskBillingMode),
    VideoResolutionPrice: normalizeJsonString(
      modelDefaults.VideoResolutionPrice
    ),
  })
  const [savedModelValues, setSavedModelValues] = useState(
    modelNormalizedDefaults.current
  )
  const modelRawDefaults = useRef(modelPricingDocuments(modelDefaults))
  const pricingPublicationPendingRef = useRef(false)

  const groupNormalizedDefaults = useRef({
    GroupRatio: normalizeJsonString(groupDefaults.GroupRatio),
    TopupGroupRatio: normalizeJsonString(groupDefaults.TopupGroupRatio),
    UserUsableGroups: normalizeJsonString(groupDefaults.UserUsableGroups),
    GroupGroupRatio: normalizeJsonString(groupDefaults.GroupGroupRatio),
    AutoGroups: normalizeJsonString(groupDefaults.AutoGroups),
    DefaultUseAutoGroup: groupDefaults.DefaultUseAutoGroup,
    GroupSpecialUsableGroup: normalizeJsonString(
      groupDefaults.GroupSpecialUsableGroup
    ),
  })
  const modelSchema = useMemo(() => createModelSchema(t), [t])
  const groupSchema = useMemo(() => createGroupSchema(t), [t])

  const modelForm = useForm<ModelFormValues>({
    resolver: zodResolver(modelSchema),
    mode: 'onChange',
    defaultValues: formatModelFormValuesForEditing(modelDefaults),
  })

  const groupForm = useForm<GroupFormValues>({
    resolver: zodResolver(groupSchema),
    mode: 'onChange',
    defaultValues: {
      ...groupDefaults,
      GroupRatio: formatJsonForTextarea(groupDefaults.GroupRatio),
      TopupGroupRatio: formatJsonForTextarea(groupDefaults.TopupGroupRatio),
      UserUsableGroups: formatJsonForTextarea(groupDefaults.UserUsableGroups),
      GroupGroupRatio: formatJsonForTextarea(groupDefaults.GroupGroupRatio),
      AutoGroups: formatJsonForTextarea(groupDefaults.AutoGroups),
      GroupSpecialUsableGroup: formatJsonForTextarea(
        groupDefaults.GroupSpecialUsableGroup
      ),
    },
  })

  useEffect(() => {
    const incomingRawDocuments = modelPricingDocuments(modelDefaults)
    if (
      pricingPublicationPendingRef.current &&
      !PRICING_DOCUMENT_KEYS.every(
        (key) => incomingRawDocuments[key] === modelRawDefaults.current[key]
      )
    ) {
      return
    }
    pricingPublicationPendingRef.current = false
    modelRawDefaults.current = incomingRawDocuments
    modelNormalizedDefaults.current = {
      ModelPrice: normalizeJsonString(modelDefaults.ModelPrice),
      ModelRatio: normalizeJsonString(modelDefaults.ModelRatio),
      CacheRatio: normalizeJsonString(modelDefaults.CacheRatio),
      CreateCacheRatio: normalizeJsonString(modelDefaults.CreateCacheRatio),
      CompletionRatio: normalizeJsonString(modelDefaults.CompletionRatio),
      ImageRatio: normalizeJsonString(modelDefaults.ImageRatio),
      AudioRatio: normalizeJsonString(modelDefaults.AudioRatio),
      AudioCompletionRatio: normalizeJsonString(
        modelDefaults.AudioCompletionRatio
      ),
      ExposeRatioEnabled: modelDefaults.ExposeRatioEnabled,
      BillingMode: normalizeJsonString(modelDefaults.BillingMode),
      BillingExpr: normalizeJsonString(modelDefaults.BillingExpr),
      TaskBillingMode: normalizeJsonString(modelDefaults.TaskBillingMode),
      VideoResolutionPrice: normalizeJsonString(
        modelDefaults.VideoResolutionPrice
      ),
    }
    setSavedModelValues(modelNormalizedDefaults.current)

    modelForm.reset(formatModelFormValuesForEditing(modelDefaults))
  }, [modelDefaults, modelForm])

  useEffect(() => {
    groupNormalizedDefaults.current = {
      GroupRatio: normalizeJsonString(groupDefaults.GroupRatio),
      TopupGroupRatio: normalizeJsonString(groupDefaults.TopupGroupRatio),
      UserUsableGroups: normalizeJsonString(groupDefaults.UserUsableGroups),
      GroupGroupRatio: normalizeJsonString(groupDefaults.GroupGroupRatio),
      AutoGroups: normalizeJsonString(groupDefaults.AutoGroups),
      DefaultUseAutoGroup: groupDefaults.DefaultUseAutoGroup,
      GroupSpecialUsableGroup: normalizeJsonString(
        groupDefaults.GroupSpecialUsableGroup
      ),
    }

    groupForm.reset({
      ...groupDefaults,
      GroupRatio: formatJsonForTextarea(groupDefaults.GroupRatio),
      TopupGroupRatio: formatJsonForTextarea(groupDefaults.TopupGroupRatio),
      UserUsableGroups: formatJsonForTextarea(groupDefaults.UserUsableGroups),
      GroupGroupRatio: formatJsonForTextarea(groupDefaults.GroupGroupRatio),
      AutoGroups: formatJsonForTextarea(groupDefaults.AutoGroups),
      GroupSpecialUsableGroup: formatJsonForTextarea(
        groupDefaults.GroupSpecialUsableGroup
      ),
    })
  }, [groupDefaults, groupForm])

  const saveModelRatios = useCallback(
    async (values: ModelFormValues) => {
      if (modelSavePendingRef.current) return
      modelSavePendingRef.current = true
      setModelSavePending(true)

      try {
        const normalized = normalizeModelFormValues(values)
        const replacement = buildPricingDocumentReplacement(
          modelPricingDocuments(modelNormalizedDefaults.current),
          modelPricingDocuments(values),
          modelRawDefaults.current
        )
        const hasPricingChanges = Object.keys(replacement.values).length > 0
        const exposeRatioChanged =
          normalized.ExposeRatioEnabled !==
          modelNormalizedDefaults.current.ExposeRatioEnabled

        if (!hasPricingChanges && !exposeRatioChanged) {
          toast.info(t('No model price changes to save'))
          return
        }

        let pricingCommitted = false
        let pricingPending = false
        let pricingConflict = false
        let pricingFailureMessage = ''
        let exposureSaved = false
        let exposureFailureMessage = ''

        if (hasPricingChanges) {
          try {
            await ensureSystemOptionsCacheBase(queryClient)
            const pricingResponse = await pricingMutation.mutateAsync({
              kind: 'replace_documents',
              target_name: '',
              values: replacement.values,
              expected_documents: replacement.expected_documents,
            })
            if (pricingResponse.committed) {
              const committedDocuments = pricingResponse.data
              const cacheAdopted = await tryAdoptCommittedPricingDocuments(
                queryClient,
                committedDocuments
              )
              modelRawDefaults.current = committedDocuments
              const committedFormValues = modelFormValuesFromDocuments(
                committedDocuments,
                normalized.ExposeRatioEnabled
              )
              const committedNormalized = normalizeModelFormValues({
                ...committedFormValues,
                ExposeRatioEnabled:
                  modelNormalizedDefaults.current.ExposeRatioEnabled,
              })
              modelNormalizedDefaults.current = committedNormalized
              setSavedModelValues(committedNormalized)
              modelForm.reset(
                formatModelFormValuesForEditing(committedFormValues)
              )
              setModelEditorRevision((revision) => revision + 1)
              pricingCommitted = true
              pricingPending =
                pricingResponse.publication_pending || !cacheAdopted
              pricingPublicationPendingRef.current = pricingPending
            } else {
              pricingFailureMessage =
                pricingResponse.message || t('Failed to update setting')
            }
          } catch (error: unknown) {
            pricingConflict =
              axios.isAxiosError(error) && error.response?.status === 409
            // 400 验证错误的可操作原因在响应体 message 里；axios 自身的
            // error.message 只是笼统的状态码描述
            pricingFailureMessage = axios.isAxiosError(error)
              ? ((error.response?.data as { message?: string } | undefined)
                  ?.message ??
                error.message)
              : t('Failed to update setting')
          }
        }

        if (exposeRatioChanged) {
          try {
            const exposureResponse = await updateSystemOption({
              key: 'ExposeRatioEnabled',
              value: normalized.ExposeRatioEnabled,
            })
            if (exposureResponse.success) {
              const committedNormalized = {
                ...modelNormalizedDefaults.current,
                ExposeRatioEnabled: normalized.ExposeRatioEnabled,
              }
              modelNormalizedDefaults.current = committedNormalized
              setSavedModelValues(committedNormalized)
              queryClient.setQueryData<SystemOptionsResponse>(
                ['system-options'],
                (current) => {
                  if (!current) return current
                  const value = String(normalized.ExposeRatioEnabled)
                  const hasExposureOption = current.data.some(
                    (option) => option.key === 'ExposeRatioEnabled'
                  )
                  return {
                    ...current,
                    data: hasExposureOption
                      ? current.data.map((option) =>
                          option.key === 'ExposeRatioEnabled'
                            ? { ...option, value }
                            : option
                        )
                      : [...current.data, { key: 'ExposeRatioEnabled', value }],
                  }
                }
              )
              exposureSaved = true
            } else {
              exposureFailureMessage =
                exposureResponse.message || t('Failed to update setting')
            }
          } catch (error: unknown) {
            exposureFailureMessage =
              error instanceof Error && error.message
                ? error.message
                : t('Failed to update setting')
          }
        }

        const allRequestedChangesSucceeded =
          (!hasPricingChanges || pricingCommitted) &&
          (!exposeRatioChanged || exposureSaved)
        if (
          allRequestedChangesSucceeded &&
          !pricingConflict &&
          !pricingPublicationPendingRef.current
        ) {
          await queryClient.invalidateQueries({ queryKey: ['system-options'] })
        }
        if (pricingConflict) {
          pricingPublicationPendingRef.current = false
          await queryClient.invalidateQueries({ queryKey: ['system-options'] })
          await queryClient.refetchQueries({ queryKey: ['system-options'] })
        }

        if (pricingCommitted) {
          if (pricingPending) {
            toast.warning(
              t(
                'Pricing was saved, but live settings are still converging. Do not retry.'
              )
            )
          } else if (!exposeRatioChanged || exposureSaved) {
            toast.success(t('Setting updated successfully'))
          }

          if (exposeRatioChanged && !exposureSaved) {
            toast.warning(
              t(
                'Pricing was saved, but ratio exposure could not be updated. Retry only ratio exposure.'
              )
            )
          }
          return
        }

        if (pricingConflict) {
          toast.error(
            t(
              exposureSaved
                ? 'Ratio exposure was saved, but pricing changed on the server. Review the refreshed pricing before saving again.'
                : 'Pricing changed on the server. Review the refreshed values before saving again.'
            )
          )
          if (exposeRatioChanged && !exposureSaved && exposureFailureMessage) {
            toast.error(exposureFailureMessage)
          }
          return
        }

        if (hasPricingChanges && pricingFailureMessage) {
          if (exposureSaved) {
            toast.warning(
              t('Ratio exposure was saved, but pricing could not be updated.')
            )
          } else {
            toast.error(pricingFailureMessage)
            if (exposeRatioChanged && exposureFailureMessage) {
              toast.error(exposureFailureMessage)
            }
          }
          return
        }

        if (exposureSaved) {
          toast.success(t('Setting updated successfully'))
        } else if (exposureFailureMessage) {
          toast.error(exposureFailureMessage)
        }
      } finally {
        modelSavePendingRef.current = false
        setModelSavePending(false)
      }
    },
    [modelForm, pricingMutation, queryClient, t]
  )

  const saveGroupRatios = useCallback(
    async (values: GroupFormValues) => {
      const normalized = {
        GroupRatio: normalizeJsonString(values.GroupRatio),
        TopupGroupRatio: normalizeJsonString(values.TopupGroupRatio),
        UserUsableGroups: normalizeJsonString(values.UserUsableGroups),
        GroupGroupRatio: normalizeJsonString(values.GroupGroupRatio),
        AutoGroups: normalizeJsonString(values.AutoGroups),
        DefaultUseAutoGroup: values.DefaultUseAutoGroup,
        GroupSpecialUsableGroup: normalizeJsonString(
          values.GroupSpecialUsableGroup
        ),
      }

      // Map form field names to API keys (most are 1:1, except GroupSpecialUsableGroup)
      const apiKeyMap: Record<string, string> = {
        GroupSpecialUsableGroup:
          'group_ratio_setting.group_special_usable_group',
      }

      const updates = (
        Object.keys(normalized) as Array<keyof typeof normalized>
      ).filter(
        (key) => normalized[key] !== groupNormalizedDefaults.current[key]
      )

      for (const key of updates) {
        const apiKey = apiKeyMap[key] || key
        await updateOption.mutateAsync({ key: apiKey, value: normalized[key] })
      }
    },
    [updateOption]
  )

  const handleResetRatios = useCallback(() => {
    setConfirmOpen(true)
  }, [])

  const { mutate: resetMutate } = resetMutation
  const handleConfirmReset = useCallback(() => {
    resetMutate()
  }, [resetMutate])

  const tabLabels: Record<RatioTabId, string> = {
    models: 'Model prices',
    'unset-models': 'Unset price models',
    groups: 'Group ratios',
    'tool-prices': 'Tool prices',
    'upstream-sync': 'Upstream price sync',
  }
  const tabsGridClass =
    {
      1: 'grid-cols-1',
      2: 'grid-cols-2',
      3: 'grid-cols-3',
      4: 'grid-cols-4',
      5: 'grid-cols-5',
    }[visibleTabs.length] ?? 'grid-cols-4'
  const defaultTab = visibleTabs[0] ?? 'models'

  const renderTabContent = (tab: RatioTabId) => {
    if (tab === 'models' || tab === 'unset-models') {
      return (
        <ModelRatioForm
          form={modelForm}
          savedValues={savedModelValues}
          onSave={saveModelRatios}
          onReset={handleResetRatios}
          isSaving={
            modelSavePending ||
            updateOption.isPending ||
            pricingMutation.isPending
          }
          isResetting={resetMutation.isPending}
          editorRevision={modelEditorRevision}
          variant={tab === 'unset-models' ? 'unset' : 'default'}
        />
      )
    }
    if (tab === 'groups') {
      return (
        <GroupRatioForm
          form={groupForm}
          onSave={saveGroupRatios}
          isSaving={updateOption.isPending}
        />
      )
    }
    if (tab === 'tool-prices') {
      return <ToolPriceSettings defaultValue={toolPricesDefault} />
    }
    return (
      <UpstreamRatioSync
        modelRatios={{
          ModelPrice: modelDefaults.ModelPrice,
          ModelRatio: modelDefaults.ModelRatio,
          CompletionRatio: modelDefaults.CompletionRatio,
          CacheRatio: modelDefaults.CacheRatio,
          CreateCacheRatio: modelDefaults.CreateCacheRatio,
          ImageRatio: modelDefaults.ImageRatio,
          AudioRatio: modelDefaults.AudioRatio,
          AudioCompletionRatio: modelDefaults.AudioCompletionRatio,
          'billing_setting.billing_mode': modelDefaults.BillingMode,
          'billing_setting.billing_expr': modelDefaults.BillingExpr,
        }}
      />
    )
  }

  const renderTabSwitcher = () => (
    <TabsList className={`grid w-fit max-w-full ${tabsGridClass}`}>
      {visibleTabs.map((tab) => (
        <TabsTrigger key={tab} value={tab}>
          {t(tabLabels[tab])}
        </TabsTrigger>
      ))}
    </TabsList>
  )

  return (
    <>
      {visibleTabs.length === 1 ? (
        <SettingsSection title={t(titleKey)}>
          {renderTabContent(defaultTab)}
        </SettingsSection>
      ) : (
        <Tabs defaultValue={defaultTab} className='h-full min-h-0 gap-6'>
          <SettingsPageTitleStatusPortal>
            {renderTabSwitcher()}
          </SettingsPageTitleStatusPortal>

          <SettingsSection title={t(titleKey)} className='min-h-0 flex-1'>
            {visibleTabs.map((tab) => (
              <TabsContent key={tab} value={tab} className='min-h-0'>
                {renderTabContent(tab)}
              </TabsContent>
            ))}
          </SettingsSection>
        </Tabs>
      )}

      <ConfirmDialog
        open={confirmOpen}
        onOpenChange={setConfirmOpen}
        title={t('Reset all model prices?')}
        desc={t(
          'This will clear custom pricing ratios and revert to upstream defaults.'
        )}
        destructive
        isLoading={resetMutation.isPending}
        handleConfirm={handleConfirmReset}
        confirmText={t('Reset')}
      />
    </>
  )
}
