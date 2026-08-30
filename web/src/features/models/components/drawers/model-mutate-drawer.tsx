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
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { ChevronDown, Loader2 } from 'lucide-react'
import { useEffect, useState, useCallback, useMemo, useRef } from 'react'
import { useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import {
  SideDrawerSection,
  sideDrawerContentClassName,
  sideDrawerFooterClassName,
  sideDrawerFormClassName,
  sideDrawerHeaderClassName,
  sideDrawerSwitchItemClassName,
} from '@/components/drawer-layout'
import { JsonEditor } from '@/components/json-editor'
import { TagInput } from '@/components/tag-input'
import { Button } from '@/components/ui/button'
import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from '@/components/ui/collapsible'
import {
  Form,
  FormControl,
  FormDescription,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from '@/components/ui/form'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { RadioGroup, RadioGroupItem } from '@/components/ui/radio-group'
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import {
  Sheet,
  SheetClose,
  SheetContent,
  SheetDescription,
  SheetFooter,
  SheetHeader,
  SheetTitle,
} from '@/components/ui/sheet'
import { Switch } from '@/components/ui/switch'
import { Textarea } from '@/components/ui/textarea'
import {
  getOptionValue,
  useSystemOptions,
} from '@/features/system-settings/hooks/use-system-options'
import { isCompleteFinitePricingNumber } from '@/features/system-settings/models/model-pricing-core'
import {
  buildModelPricingSelection,
  type ModelPricingSelection,
} from '@/features/system-settings/models/model-pricing-persistence'
import {
  ensureSystemOptionsCacheBase,
  tryAdoptCommittedPricingDocuments,
} from '@/features/system-settings/models/pricing-document-cache'
import { VideoResolutionPriceEditor } from '@/features/system-settings/models/video-resolution-price-editor'
import {
  parseVideoResolutionPriceOption,
  sanitizeVideoResolutionPriceMap,
  validateVideoResolutionPriceRows,
  videoResolutionPriceRows,
  type VideoResolutionPriceRow,
} from '@/features/system-settings/models/video-resolution-pricing'
import type { ModelSettings } from '@/features/system-settings/types'
import { safeJsonParse } from '@/features/system-settings/utils/json-parser'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

import { createModel, updateModel, getModel, getVendors } from '../../api'
import { getNameRuleOptions, ENDPOINT_TEMPLATES } from '../../constants'
import { modelsQueryKeys, vendorsQueryKeys, parseModelTags } from '../../lib'
import {
  createExtendedModelFormSchema,
  type ExtendedModelFormValues,
} from '../../lib/model-mutate-schema'
import type { Model } from '../../types'

type PricingMode = 'per-token' | 'per-request' | 'video_resolution'
type PricingSubMode = 'ratio' | 'price'

type PricingFields = Pick<
  ExtendedModelFormValues,
  | 'price'
  | 'ratio'
  | 'cacheRatio'
  | 'createCacheRatio'
  | 'completionRatio'
  | 'imageRatio'
  | 'audioRatio'
  | 'audioCompletionRatio'
>

// Form state describing the pricing currently configured for one model name.
type PricingConfig = {
  mode: PricingMode
  fields: PricingFields
  promptPrice: string
  completionPrice: string
  advancedOpen: boolean
}

const EMPTY_PRICING_FIELDS: PricingFields = {
  price: '',
  ratio: '',
  cacheRatio: '',
  createCacheRatio: '',
  completionRatio: '',
  imageRatio: '',
  audioRatio: '',
  audioCompletionRatio: '',
}

const EMPTY_PRICING_CONFIG: PricingConfig = {
  mode: 'per-token',
  fields: EMPTY_PRICING_FIELDS,
  promptPrice: '',
  completionPrice: '',
  advancedOpen: false,
}

function lookupModelRatio(
  rawMap: string,
  modelName: string
): number | undefined {
  return safeJsonParse<Record<string, number>>(rawMap, {
    fallback: {},
    silent: true,
  })[modelName]
}

// 视频任务计费单位（per_second/per_call），与模型定价页共用 TaskBillingMode 配置
function lookupTaskBillingMode(
  settings: ModelSettings | null,
  modelName: string
): string {
  if (!settings || !modelName) return ''
  return (
    safeJsonParse<Record<string, string>>(settings.TaskBillingMode, {
      fallback: {},
      silent: true,
    })[modelName] || ''
  )
}

// Pricing is not stored on the model row: it lives in system options as
// model-name keyed JSON maps, so the drawer reads those maps to populate its
// optional pricing selection. Unchanged pricing is omitted from the request.
function readPricingConfig(
  settings: ModelSettings | null,
  modelName: string
): PricingConfig {
  if (!settings || !modelName) return EMPTY_PRICING_CONFIG

  const price = lookupModelRatio(settings.ModelPrice, modelName)
  const ratio = lookupModelRatio(settings.ModelRatio, modelName)
  const cacheRatio = lookupModelRatio(settings.CacheRatio, modelName)
  const createCacheRatio = lookupModelRatio(
    settings.CreateCacheRatio,
    modelName
  )
  const completionRatio = lookupModelRatio(settings.CompletionRatio, modelName)
  const imageRatio = lookupModelRatio(settings.ImageRatio, modelName)
  const audioRatio = lookupModelRatio(settings.AudioRatio, modelName)
  const audioCompletionRatio = lookupModelRatio(
    settings.AudioCompletionRatio,
    modelName
  )

  // A fixed per-request price wins outright at billing time (see
  // GetModelRatioOrPrice), so a name that has one is shown, and saved back, as
  // price-only: the ratios alongside it are dead weight.
  if (price !== undefined && price !== null) {
    return {
      ...EMPTY_PRICING_CONFIG,
      mode: 'per-request',
      fields: { ...EMPTY_PRICING_FIELDS, price: price.toString() },
    }
  }

  let promptPrice = ''
  let completionPrice = ''
  if (ratio !== undefined && ratio !== null) {
    const tokenPrice = ratio * 2
    promptPrice = tokenPrice.toString()
    if (completionRatio !== undefined && completionRatio !== null) {
      completionPrice = (tokenPrice * completionRatio).toString()
    }
  }

  return {
    mode: 'per-token',
    fields: {
      price: '',
      ratio: ratio?.toString() || '',
      cacheRatio: cacheRatio?.toString() || '',
      createCacheRatio: createCacheRatio?.toString() || '',
      completionRatio: completionRatio?.toString() || '',
      imageRatio: imageRatio?.toString() || '',
      audioRatio: audioRatio?.toString() || '',
      audioCompletionRatio: audioCompletionRatio?.toString() || '',
    },
    promptPrice,
    completionPrice,
    // Configured is not the same as non-zero: a 0 ratio (free cache reads, for
    // instance) still has to be visible rather than hidden behind the collapse.
    advancedOpen: [
      cacheRatio,
      createCacheRatio,
      imageRatio,
      audioRatio,
      audioCompletionRatio,
    ].some((value) => value !== undefined && value !== null),
  }
}

function pricingDraftSignature(
  mode: PricingMode,
  fields: PricingFields,
  taskBillingMode: string,
  resolutionPrices: Record<string, number>
): string {
  return JSON.stringify({
    mode,
    fields,
    taskBillingMode,
    resolutionPrices: sanitizeVideoResolutionPriceMap(resolutionPrices),
  })
}

type ModelMutateDrawerProps = {
  open: boolean
  onOpenChange: (open: boolean) => void
  currentRow?: Model | null
}

export function ModelMutateDrawer({
  open,
  onOpenChange,
  currentRow,
}: ModelMutateDrawerProps) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const currentModelId = currentRow?.id
  const isEditing = Boolean(currentModelId)
  const userRole = useAuthStore((state) => state.auth.user?.role ?? ROLE.GUEST)
  const canManagePricing = userRole >= ROLE.SUPER_ADMIN
  const [isSubmitting, setIsSubmitting] = useState(false)
  const submittingRef = useRef(false)
  const [pricingMode, setPricingMode] = useState<PricingMode>('per-token')
  const [pricingSubMode, setPricingSubMode] = useState<PricingSubMode>('ratio')
  // '' 表示未显式配置，走系统默认（按秒）
  const [taskBillingMode, setTaskBillingMode] = useState('')
  const [resolutionRows, setResolutionRows] = useState<
    VideoResolutionPriceRow[]
  >([])
  const resolutionValidation = useMemo(
    () => validateVideoResolutionPriceRows(resolutionRows),
    [resolutionRows]
  )
  const [advancedOpen, setAdvancedOpen] = useState(false)
  const [promptPrice, setPromptPrice] = useState('')
  const [completionPrice, setCompletionPrice] = useState('')
  const loadedPricingSignatureRef = useRef('')
  // Keep a ref so the load effect can read the latest modelSettings without
  // depending on it: modelSettings is a fresh object on every system-options
  // refetch, and including it in the deps would reset the form under the user.
  const modelSettingsRef = useRef<ModelSettings | null>(null)
  const extendedModelFormSchema = useMemo(
    () => createExtendedModelFormSchema(t),
    [t]
  )

  // Fetch vendors for dropdown
  const { data: vendorsData } = useQuery({
    queryKey: vendorsQueryKeys.list(),
    queryFn: () => getVendors({ page_size: 1000 }),
    enabled: open,
  })

  const vendors = vendorsData?.data?.items || []

  // Fetch model detail if editing
  const { data: modelData } = useQuery({
    queryKey: modelsQueryKeys.detail(currentModelId || 0),
    queryFn: () => {
      if (!currentModelId) {
        throw new Error('Model ID is required')
      }
      return getModel(currentModelId)
    },
    enabled: open && isEditing,
  })

  // Fetch system options for ratio configuration
  const { data: systemOptionsData } = useSystemOptions()

  // Get model settings from system options
  const modelSettings = useMemo(() => {
    if (!systemOptionsData?.data) return null
    const defaultModelSettings: ModelSettings = {
      'global.pass_through_request_enabled': false,
      'global.thinking_model_blacklist': '[]',
      'global.chat_completions_to_responses_policy': '{}',
      'general_setting.ping_interval_enabled': false,
      'general_setting.ping_interval_seconds': 60,
      'gemini.safety_settings': '',
      'gemini.version_settings': '',
      'gemini.supported_imagine_models': '',
      'gemini.thinking_adapter_enabled': false,
      'gemini.thinking_adapter_budget_tokens_percentage': 0.6,
      'gemini.function_call_thought_signature_enabled': false,
      'gemini.remove_function_response_id_enabled': true,
      'claude.model_headers_settings': '',
      'claude.default_max_tokens': '',
      'claude.thinking_adapter_enabled': true,
      'claude.thinking_adapter_budget_tokens_percentage': 0.8,
      ModelPrice: '',
      ModelRatio: '',
      CacheRatio: '',
      CompletionRatio: '',
      ImageRatio: '',
      AudioRatio: '',
      AudioCompletionRatio: '',
      ExposeRatioEnabled: false,
      'billing_setting.billing_mode': '{}',
      'billing_setting.billing_expr': '{}',
      TaskBillingMode: '{}',
      VideoResolutionPrice: '{}',
      'tool_price_setting.prices': '{}',
      TopupGroupRatio: '',
      GroupRatio: '',
      UserUsableGroups: '',
      GroupGroupRatio: '',
      AutoGroups: '',
      DefaultUseAutoGroup: false,
      CreateCacheRatio: '',
      'group_ratio_setting.group_special_usable_group': '{}',
      'grok.violation_deduction_enabled': false,
      'grok.violation_deduction_amount': 0,
      RetryTimes: 0,
      ChannelDisableThreshold: '',
      AutomaticDisableChannelEnabled: false,
      AutomaticEnableChannelEnabled: false,
      AutomaticDisableKeywords: '',
      AutomaticDisableStatusCodes: '401',
      AutomaticRetryStatusCodes:
        '100-199,300-399,401-407,409-499,500-503,505-523,525-599',
      'monitor_setting.auto_test_channel_enabled': false,
      'monitor_setting.auto_test_channel_minutes': 10,
      'monitor_setting.channel_test_mode': 'scheduled_all',
      'channel_affinity_setting.enabled': false,
      'channel_affinity_setting.switch_on_success': true,
      'channel_affinity_setting.keep_on_channel_disabled': false,
      'channel_affinity_setting.max_entries': 100000,
      'channel_affinity_setting.default_ttl_seconds': 3600,
      'channel_affinity_setting.rules': '[]',
      'model_deployment.ionet.api_key': '',
      'model_deployment.ionet.enabled': false,
    }
    return getOptionValue(systemOptionsData.data, defaultModelSettings)
  }, [systemOptionsData])

  // The load effect keys off this boolean, not the object: it re-runs once
  // when the settings first arrive (so a drawer opened before that still gets
  // its pricing prefilled), while later refetches only produce a new object
  // reference and must not reset a form the user may be editing.
  const hasModelSettings = modelSettings !== null
  useEffect(() => {
    modelSettingsRef.current = modelSettings
  })

  const form = useForm<ExtendedModelFormValues>({
    resolver: zodResolver(extendedModelFormSchema),
    defaultValues: {
      model_name: '',
      description: '',
      icon: '',
      tags: [],
      vendor_id: undefined,
      endpoints: '',
      name_rule: 0,
      status: true,
      sync_official: true,
      price: '',
      ratio: '',
      cacheRatio: '',
      createCacheRatio: '',
      completionRatio: '',
      imageRatio: '',
      audioRatio: '',
      audioCompletionRatio: '',
    },
  })

  const handlePromptPriceChange = (value: string) => {
    setPromptPrice(value)
    if (value && isCompleteFinitePricingNumber(value)) {
      const ratio = Number(value) / 2
      form.setValue('ratio', ratio.toString())
    } else {
      form.setValue('ratio', value)
    }
  }

  const handleCompletionPriceChange = (value: string) => {
    setCompletionPrice(value)
    if (
      value &&
      isCompleteFinitePricingNumber(value) &&
      promptPrice &&
      isCompleteFinitePricingNumber(promptPrice) &&
      Number(promptPrice) > 0
    ) {
      const completionRatio = Number(value) / Number(promptPrice)
      form.setValue('completionRatio', completionRatio.toString())
    } else {
      form.setValue('completionRatio', value)
    }
  }

  // Load model data for editing and ratio configuration
  useEffect(() => {
    if (open && isEditing && modelData?.data) {
      const model = modelData.data
      const pricing = readPricingConfig(
        modelSettingsRef.current,
        model.model_name
      )
      setPromptPrice(pricing.promptPrice)
      setCompletionPrice(pricing.completionPrice)
      setAdvancedOpen(pricing.advancedOpen)
      const savedTaskBillingMode = lookupTaskBillingMode(
        modelSettingsRef.current,
        model.model_name
      )
      setTaskBillingMode(savedTaskBillingMode)
      const savedResolutionPrices = parseVideoResolutionPriceOption(
        modelSettingsRef.current?.VideoResolutionPrice
      )[model.model_name]
      setResolutionRows(videoResolutionPriceRows(savedResolutionPrices))
      const loadedMode = savedResolutionPrices
        ? 'video_resolution'
        : pricing.mode
      setPricingMode(loadedMode)
      loadedPricingSignatureRef.current = pricingDraftSignature(
        loadedMode,
        pricing.fields,
        savedTaskBillingMode,
        savedResolutionPrices || {}
      )
      form.reset({
        id: model.id,
        model_name: model.model_name,
        description: model.description || '',
        icon: model.icon || '',
        tags: parseModelTags(model.tags),
        vendor_id: model.vendor_id,
        endpoints: model.endpoints || '',
        name_rule: model.name_rule || 0,
        status: model.status === 1,
        sync_official: model.sync_official === 1,
        ...pricing.fields,
      })
    } else if (open && !isEditing) {
      // Pre-fill model name if passed from missing models, along with any
      // pricing that name already has, so the user edits it instead of being
      // shown an empty form that hides existing configuration.
      const modelName = currentRow?.model_name || ''
      const pricing = readPricingConfig(modelSettingsRef.current, modelName)
      setPricingSubMode('ratio')
      setPromptPrice(pricing.promptPrice)
      setCompletionPrice(pricing.completionPrice)
      setAdvancedOpen(pricing.advancedOpen)
      const savedTaskBillingMode = lookupTaskBillingMode(
        modelSettingsRef.current,
        modelName
      )
      setTaskBillingMode(savedTaskBillingMode)
      const savedResolutionPrices = parseVideoResolutionPriceOption(
        modelSettingsRef.current?.VideoResolutionPrice
      )[modelName]
      setResolutionRows(videoResolutionPriceRows(savedResolutionPrices))
      const loadedMode = savedResolutionPrices
        ? 'video_resolution'
        : pricing.mode
      setPricingMode(loadedMode)
      loadedPricingSignatureRef.current = pricingDraftSignature(
        loadedMode,
        pricing.fields,
        savedTaskBillingMode,
        savedResolutionPrices || {}
      )
      form.reset({
        model_name: modelName,
        description: '',
        icon: '',
        tags: [],
        vendor_id: undefined,
        endpoints: '',
        name_rule: 0,
        status: true,
        sync_official: true,
        ...pricing.fields,
      })
    }
  }, [open, isEditing, modelData, currentRow, form, hasModelSettings])

  const onSubmit = useCallback(
    async (values: ExtendedModelFormValues): Promise<void> => {
      if (submittingRef.current) return
      submittingRef.current = true

      if (
        canManagePricing &&
        pricingMode === 'video_resolution' &&
        (resolutionValidation.prices === null ||
          Object.keys(resolutionValidation.prices).length === 0)
      ) {
        toast.error(t('Fix the resolution prices before saving'))
        submittingRef.current = false
        return
      }
      if (
        canManagePricing &&
        pricingMode === 'per-request' &&
        !isCompleteFinitePricingNumber(values.price)
      ) {
        form.setError('price', {
          message: t('Please enter a valid number'),
        })
        toast.error(t('Price is required'))
        submittingRef.current = false
        return
      }
      if (
        canManagePricing &&
        pricingMode === 'per-token' &&
        !isCompleteFinitePricingNumber(values.ratio)
      ) {
        form.setError('ratio', {
          message: t('Please enter a valid number'),
        })
        submittingRef.current = false
        return
      }
      setIsSubmitting(true)
      try {
        const submittedModelName =
          isEditing && !canManagePricing
            ? (modelData?.data?.model_name ??
              currentRow?.model_name ??
              values.model_name)
            : values.model_name
        const submitData = {
          ...values,
          model_name: submittedModelName,
          id: isEditing ? currentModelId : undefined,
          tags: Array.isArray(values.tags) ? values.tags.join(',') : '',
          status: values.status ? 1 : 0,
          sync_official: values.sync_official ? 1 : 0,
        }

        const {
          price,
          ratio,
          cacheRatio,
          createCacheRatio,
          completionRatio,
          imageRatio,
          audioRatio,
          audioCompletionRatio,
          ...modelPayload
        } = submitData

        const pricingFields: PricingFields = {
          price,
          ratio,
          cacheRatio,
          createCacheRatio,
          completionRatio,
          imageRatio,
          audioRatio,
          audioCompletionRatio,
        }
        const resolutionPrices = resolutionValidation.prices ?? {}
        const currentPricingSignature = pricingDraftSignature(
          pricingMode,
          pricingFields,
          taskBillingMode,
          resolutionPrices
        )
        let pricing: ModelPricingSelection | undefined
        if (
          canManagePricing &&
          currentPricingSignature !== loadedPricingSignatureRef.current
        ) {
          pricing =
            pricingMode === 'video_resolution'
              ? {
                  mode: 'video_resolution',
                  resolution_prices: resolutionPrices,
                }
              : buildModelPricingSelection({
                  name: submittedModelName,
                  billingMode: pricingMode,
                  ...pricingFields,
                  taskBillingMode,
                })
        }

        const persistedModelName =
          modelData?.data?.model_name ?? currentRow?.model_name
        const usesPricingCommand =
          Boolean(pricing) ||
          (isEditing &&
            persistedModelName !== undefined &&
            submittedModelName !== persistedModelName)
        if (usesPricingCommand) {
          await ensureSystemOptionsCacheBase(queryClient)
        }

        const response =
          isEditing && currentModelId
            ? await updateModel({
                ...modelPayload,
                id: currentModelId,
                ...(pricing ? { pricing } : {}),
              })
            : await createModel({
                ...modelPayload,
                ...(pricing ? { pricing } : {}),
              })

        if (response.success) {
          let cacheAdopted = true
          if (response.pricing_documents) {
            cacheAdopted = await tryAdoptCommittedPricingDocuments(
              queryClient,
              response.pricing_documents
            )
          }
          if (response.publication_pending || !cacheAdopted) {
            toast.warning(
              t(
                'Pricing was saved, but live settings are still converging. Do not retry.'
              )
            )
          } else {
            toast.success(
              t(
                isEditing
                  ? 'Model updated successfully'
                  : 'Model created successfully'
              )
            )
          }
          queryClient.invalidateQueries({ queryKey: modelsQueryKeys.lists() })
          if (!response.publication_pending && cacheAdopted) {
            queryClient.invalidateQueries({ queryKey: ['system-options'] })
          }
          onOpenChange(false)
        } else {
          toast.error(response.message || 'Operation failed')
        }
      } catch (error: unknown) {
        toast.error((error as Error)?.message || 'Operation failed')
      } finally {
        submittingRef.current = false
        setIsSubmitting(false)
      }
    },
    [
      isEditing,
      currentModelId,
      queryClient,
      onOpenChange,
      pricingMode,
      taskBillingMode,
      resolutionValidation,
      canManagePricing,
      modelData,
      currentRow,
      form,
      t,
    ]
  )

  const handleFillEndpointTemplate = (templateKey: string) => {
    const template = ENDPOINT_TEMPLATES[templateKey]
    if (template) {
      const templateJson = JSON.stringify({ [templateKey]: template }, null, 2)
      form.setValue('endpoints', templateJson)
    }
  }

  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent className={sideDrawerContentClassName('sm:max-w-2xl')}>
        <SheetHeader className={sideDrawerHeaderClassName()}>
          <SheetTitle>
            {isEditing ? t('Edit Model') : t('Create Model')}
          </SheetTitle>
          <SheetDescription>
            {isEditing
              ? t("Update model configuration and click save when you're done.")
              : t(
                  'Add a new model to the system by providing the necessary information.'
                )}
          </SheetDescription>
        </SheetHeader>

        <Form {...form}>
          <form
            id='model-form'
            onSubmit={form.handleSubmit(
              onSubmit as Parameters<typeof form.handleSubmit>[0]
            )}
            className={sideDrawerFormClassName()}
          >
            {/* Basic Information */}
            <SideDrawerSection>
              <h3 className='text-sm font-semibold'>
                {t('Basic Information')}
              </h3>

              <FormField
                control={form.control}
                name='model_name'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('Model Name *')}</FormLabel>
                    <FormControl>
                      <Input
                        placeholder={t('gpt-4, claude-3-opus, etc.')}
                        {...field}
                        disabled={isEditing && !canManagePricing}
                      />
                    </FormControl>
                    <FormDescription>
                      {t('The unique identifier for this model')}
                    </FormDescription>
                    <FormMessage />
                  </FormItem>
                )}
              />

              <FormField
                control={form.control}
                name='description'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('Description')}</FormLabel>
                    <FormControl>
                      <Textarea
                        placeholder={t('Describe this model...')}
                        rows={3}
                        {...field}
                      />
                    </FormControl>
                    <FormMessage />
                  </FormItem>
                )}
              />

              <FormField
                control={form.control}
                name='icon'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('Icon')}</FormLabel>
                    <FormControl>
                      <Input
                        placeholder={t('OpenAI, Anthropic, etc.')}
                        {...field}
                      />
                    </FormControl>
                    <FormDescription className='text-xs'>
                      {t('@lobehub/icons key')}
                    </FormDescription>
                    <FormMessage />
                  </FormItem>
                )}
              />

              <FormField
                control={form.control}
                name='vendor_id'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('Vendor')}</FormLabel>
                    <Select
                      items={vendors.map((vendor) => ({
                        value: String(vendor.id),
                        label: vendor.name,
                      }))}
                      onValueChange={(value) =>
                        field.onChange(
                          value ? Number.parseInt(value) : undefined
                        )
                      }
                      value={field.value ? String(field.value) : undefined}
                    >
                      <FormControl>
                        <SelectTrigger>
                          <SelectValue placeholder={t('Select vendor')} />
                        </SelectTrigger>
                      </FormControl>
                      <SelectContent alignItemWithTrigger={false}>
                        <SelectGroup>
                          {vendors.map((vendor) => (
                            <SelectItem
                              key={vendor.id}
                              value={String(vendor.id)}
                            >
                              {vendor.name}
                            </SelectItem>
                          ))}
                        </SelectGroup>
                      </SelectContent>
                    </Select>
                    <FormMessage />
                  </FormItem>
                )}
              />

              <FormField
                control={form.control}
                name='tags'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('Tags')}</FormLabel>
                    <FormControl>
                      <TagInput
                        value={field.value || []}
                        onChange={field.onChange}
                        placeholder={t('Add tags...')}
                      />
                    </FormControl>
                    <FormDescription>
                      {t('Press Enter or comma to add tags')}
                    </FormDescription>
                    <FormMessage />
                  </FormItem>
                )}
              />
            </SideDrawerSection>

            {/* Matching Configuration */}
            <SideDrawerSection>
              <h3 className='text-sm font-semibold'>{t('Matching Rules')}</h3>

              <FormField
                control={form.control}
                name='name_rule'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('Name Rule')}</FormLabel>
                    <FormControl>
                      <RadioGroup
                        onValueChange={(value) =>
                          field.onChange(Number.parseInt(value))
                        }
                        value={String(field.value)}
                        className='grid grid-cols-2 gap-4'
                      >
                        {getNameRuleOptions(t).map((option) => (
                          <div
                            key={option.value}
                            className='flex items-center space-x-2'
                          >
                            <RadioGroupItem
                              value={String(option.value)}
                              id={`rule-${option.value}`}
                            />
                            <Label
                              htmlFor={`rule-${option.value}`}
                              className='cursor-pointer font-normal'
                            >
                              {option.label}
                            </Label>
                          </div>
                        ))}
                      </RadioGroup>
                    </FormControl>
                    <FormDescription>
                      {t('How this model name should match requests')}
                    </FormDescription>
                    <FormMessage />
                  </FormItem>
                )}
              />
            </SideDrawerSection>

            {/* Endpoints Configuration */}
            <SideDrawerSection>
              <div className='flex items-center justify-between'>
                <h3 className='text-sm font-semibold'>{t('Endpoints')}</h3>
                <Select<string>
                  items={Object.keys(ENDPOINT_TEMPLATES).map((key) => ({
                    value: key,
                    label: key,
                  }))}
                  onValueChange={(v) =>
                    v !== null && handleFillEndpointTemplate(v)
                  }
                >
                  <SelectTrigger size='sm' className='w-[200px]'>
                    <SelectValue placeholder={t('Load template...')} />
                  </SelectTrigger>
                  <SelectContent alignItemWithTrigger={false}>
                    <SelectGroup>
                      {Object.keys(ENDPOINT_TEMPLATES).map((key) => (
                        <SelectItem key={key} value={key}>
                          {key}
                        </SelectItem>
                      ))}
                    </SelectGroup>
                  </SelectContent>
                </Select>
              </div>

              <FormField
                control={form.control}
                name='endpoints'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('Endpoint Configuration')}</FormLabel>
                    <FormControl>
                      <JsonEditor
                        value={field.value || ''}
                        onChange={field.onChange}
                        keyPlaceholder='endpoint_type'
                        valuePlaceholder='{"path": "/v1/...", "method": "POST"}'
                        keyLabel='Endpoint Type'
                        valueLabel='Configuration'
                        valueType='any'
                        emptyMessage={t(
                          'No endpoints configured. Switch to JSON mode or add rows to define endpoints.'
                        )}
                      />
                    </FormControl>
                    <FormDescription>
                      {t('Define API endpoints for this model (JSON format)')}
                    </FormDescription>
                    <FormMessage />
                  </FormItem>
                )}
              />
            </SideDrawerSection>

            {/* Pricing Configuration */}
            {canManagePricing ? (
              <SideDrawerSection>
                <h3 className='text-sm font-semibold'>
                  {t('Pricing Configuration')}
                </h3>

                <div className='space-y-4'>
                  <Label>{t('Pricing mode')}</Label>
                  <RadioGroup
                    value={pricingMode}
                    onValueChange={(value) =>
                      setPricingMode(value as PricingMode)
                    }
                  >
                    <div className='flex items-center space-x-2'>
                      <RadioGroupItem value='per-token' id='per-token' />
                      <Label htmlFor='per-token' className='font-normal'>
                        {t('Per-token (ratio based)')}
                      </Label>
                    </div>
                    <div className='flex items-center space-x-2'>
                      <RadioGroupItem value='per-request' id='per-request' />
                      <Label htmlFor='per-request' className='font-normal'>
                        {t('Per-request (fixed price)')}
                      </Label>
                    </div>
                    <div className='flex items-center space-x-2'>
                      <RadioGroupItem
                        value='video_resolution'
                        id='video_resolution'
                      />
                      <Label htmlFor='video_resolution' className='font-normal'>
                        {t('Video resolution')}
                      </Label>
                    </div>
                  </RadioGroup>
                </div>

                {pricingMode === 'video_resolution' ? (
                  <VideoResolutionPriceEditor
                    rows={resolutionRows}
                    errorsByRowId={resolutionValidation.errorsByRowId}
                    disabled={isSubmitting}
                    onChange={setResolutionRows}
                  />
                ) : null}

                {pricingMode === 'per-request' && (
                  <>
                    <FormField
                      control={form.control}
                      name='price'
                      render={({ field }) => (
                        <FormItem>
                          <FormLabel>{t('Fixed price (USD)')}</FormLabel>
                          <FormControl>
                            <Input
                              type='text'
                              placeholder='0.01'
                              {...field}
                              onChange={field.onChange}
                            />
                          </FormControl>
                          <FormDescription>
                            {t(
                              'Cost in USD per request, regardless of tokens used.'
                            )}
                          </FormDescription>
                          <FormMessage />
                        </FormItem>
                      )}
                    />

                    <div className='space-y-2'>
                      <Label>{t('Video task billing unit')}</Label>
                      <Select
                        items={[
                          { value: '', label: t('Default (per second)') },
                          {
                            value: 'per_second',
                            label: t('Per second (× duration)'),
                          },
                          { value: 'per_call', label: t('Per task (fixed)') },
                        ]}
                        value={taskBillingMode}
                        onValueChange={(value) =>
                          value !== null && setTaskBillingMode(value)
                        }
                      >
                        <SelectTrigger className='w-full'>
                          <SelectValue />
                        </SelectTrigger>
                        <SelectContent alignItemWithTrigger={false}>
                          <SelectGroup>
                            <SelectItem value=''>
                              {t('Default (per second)')}
                            </SelectItem>
                            <SelectItem value='per_second'>
                              {t('Per second (× duration)')}
                            </SelectItem>
                            <SelectItem value='per_call'>
                              {t('Per task (fixed)')}
                            </SelectItem>
                          </SelectGroup>
                        </SelectContent>
                      </Select>
                      <p className='text-muted-foreground text-[0.8rem]'>
                        {taskBillingMode === 'per_call'
                          ? t(
                              'The fixed price is charged once per video task, regardless of its duration.'
                            )
                          : t(
                              'The fixed price is multiplied by the video duration in seconds.'
                            )}
                      </p>
                    </div>
                  </>
                )}

                {pricingMode === 'per-token' && (
                  <>
                    <div className='space-y-4'>
                      <Label>{t('Input mode')}</Label>
                      <RadioGroup
                        value={pricingSubMode}
                        onValueChange={(value) =>
                          setPricingSubMode(value as PricingSubMode)
                        }
                      >
                        <div className='flex items-center space-x-2'>
                          <RadioGroupItem value='ratio' id='ratio' />
                          <Label htmlFor='ratio' className='font-normal'>
                            {t('Ratio mode')}
                          </Label>
                        </div>
                        <div className='flex items-center space-x-2'>
                          <RadioGroupItem value='price' id='price' />
                          <Label htmlFor='price' className='font-normal'>
                            {t('Price mode (USD per 1M tokens)')}
                          </Label>
                        </div>
                      </RadioGroup>
                    </div>

                    {pricingSubMode === 'ratio' ? (
                      <>
                        <FormField
                          control={form.control}
                          name='ratio'
                          render={({ field }) => (
                            <FormItem>
                              <FormLabel>{t('Model ratio')}</FormLabel>
                              <FormControl>
                                <Input
                                  type='text'
                                  placeholder='1.0'
                                  {...field}
                                  onChange={(e) => {
                                    const value = e.target.value
                                    field.onChange(value)
                                    if (
                                      value &&
                                      isCompleteFinitePricingNumber(value)
                                    ) {
                                      setPromptPrice(
                                        (Number(value) * 2).toString()
                                      )
                                    } else {
                                      setPromptPrice('')
                                    }
                                  }}
                                />
                              </FormControl>
                              <FormDescription>
                                {field.value &&
                                isCompleteFinitePricingNumber(field.value)
                                  ? `Calculated price: $${(Number(field.value) * 2).toFixed(4)} per 1M tokens`
                                  : t('Multiplier for prompt tokens.')}
                              </FormDescription>
                              <FormMessage />
                            </FormItem>
                          )}
                        />

                        <FormField
                          control={form.control}
                          name='completionRatio'
                          render={({ field }) => (
                            <FormItem>
                              <FormLabel>{t('Completion ratio')}</FormLabel>
                              <FormControl>
                                <Input
                                  type='text'
                                  placeholder='1.0'
                                  {...field}
                                  onChange={(e) => {
                                    const value = e.target.value
                                    field.onChange(value)
                                    const ratio = form.getValues('ratio')
                                    if (
                                      value &&
                                      isCompleteFinitePricingNumber(value) &&
                                      ratio &&
                                      isCompleteFinitePricingNumber(ratio)
                                    ) {
                                      const compPrice =
                                        Number(ratio) * 2 * Number(value)
                                      setCompletionPrice(compPrice.toString())
                                    } else {
                                      setCompletionPrice('')
                                    }
                                  }}
                                />
                              </FormControl>
                              <FormDescription>
                                {field.value &&
                                isCompleteFinitePricingNumber(field.value) &&
                                promptPrice &&
                                isCompleteFinitePricingNumber(promptPrice)
                                  ? `Calculated price: $${(Number(promptPrice) * Number(field.value)).toFixed(4)} per 1M tokens`
                                  : t('Multiplier for completion tokens.')}
                              </FormDescription>
                              <FormMessage />
                            </FormItem>
                          )}
                        />
                      </>
                    ) : (
                      <div className='space-y-4'>
                        <div className='space-y-2'>
                          <Label>{t('Prompt price ($/1M tokens)')}</Label>
                          <Input
                            type='text'
                            placeholder='2.0'
                            value={promptPrice}
                            onChange={(e) =>
                              handlePromptPriceChange(e.target.value)
                            }
                          />
                          <p className='text-muted-foreground text-sm'>
                            {promptPrice &&
                            isCompleteFinitePricingNumber(promptPrice)
                              ? `Calculated ratio: ${(Number(promptPrice) / 2).toFixed(4)}`
                              : t('Enter Input price to calculate ratio')}
                          </p>
                          {promptPrice &&
                          !isCompleteFinitePricingNumber(promptPrice) ? (
                            <p className='text-destructive text-sm'>
                              {t('Please enter a valid number')}
                            </p>
                          ) : null}
                        </div>

                        <div className='space-y-2'>
                          <Label>{t('Completion price ($/1M tokens)')}</Label>
                          <Input
                            type='text'
                            placeholder='4.0'
                            value={completionPrice}
                            onChange={(e) =>
                              handleCompletionPriceChange(e.target.value)
                            }
                          />
                          <p className='text-muted-foreground text-sm'>
                            {completionPrice &&
                            isCompleteFinitePricingNumber(completionPrice) &&
                            promptPrice &&
                            isCompleteFinitePricingNumber(promptPrice) &&
                            Number(promptPrice) > 0
                              ? `Calculated ratio: ${(Number(completionPrice) / Number(promptPrice)).toFixed(4)}`
                              : t('Enter Completion price to calculate ratio')}
                          </p>
                          {completionPrice &&
                          !isCompleteFinitePricingNumber(completionPrice) ? (
                            <p className='text-destructive text-sm'>
                              {t('Please enter a valid number')}
                            </p>
                          ) : null}
                        </div>
                      </div>
                    )}

                    <Collapsible
                      open={advancedOpen}
                      onOpenChange={setAdvancedOpen}
                    >
                      <CollapsibleTrigger
                        render={
                          <Button
                            type='button'
                            variant='outline'
                            className='flex w-full items-center justify-between'
                          />
                        }
                      >
                        {t('Advanced options')}
                        <ChevronDown
                          className={`h-4 w-4 transition-transform duration-200 ${
                            advancedOpen ? 'rotate-180' : ''
                          }`}
                        />
                      </CollapsibleTrigger>
                      <CollapsibleContent className='flex flex-col gap-4 pt-4'>
                        <FormField
                          control={form.control}
                          name='cacheRatio'
                          render={({ field }) => (
                            <FormItem>
                              <FormLabel>{t('Cache ratio')}</FormLabel>
                              <FormControl>
                                <Input
                                  type='text'
                                  placeholder='0.1'
                                  {...field}
                                  onChange={field.onChange}
                                />
                              </FormControl>
                              <FormDescription>
                                {t('Discount ratio for cache hits.')}
                              </FormDescription>
                              <FormMessage />
                            </FormItem>
                          )}
                        />

                        <FormField
                          control={form.control}
                          name='createCacheRatio'
                          render={({ field }) => (
                            <FormItem>
                              <FormLabel>{t('Create cache ratio')}</FormLabel>
                              <FormControl>
                                <Input
                                  type='text'
                                  placeholder='1.0'
                                  {...field}
                                  onChange={field.onChange}
                                />
                              </FormControl>
                              <FormDescription>
                                {t(
                                  'Ratio applied when creating cache entries for supported models.'
                                )}
                              </FormDescription>
                              <FormMessage />
                            </FormItem>
                          )}
                        />

                        <FormField
                          control={form.control}
                          name='imageRatio'
                          render={({ field }) => (
                            <FormItem>
                              <FormLabel>{t('Image ratio')}</FormLabel>
                              <FormControl>
                                <Input
                                  type='text'
                                  placeholder='1.0'
                                  {...field}
                                  onChange={field.onChange}
                                />
                              </FormControl>
                              <FormDescription>
                                {t('Multiplier for image processing.')}
                              </FormDescription>
                              <FormMessage />
                            </FormItem>
                          )}
                        />

                        <FormField
                          control={form.control}
                          name='audioRatio'
                          render={({ field }) => (
                            <FormItem>
                              <FormLabel>{t('Audio ratio')}</FormLabel>
                              <FormControl>
                                <Input
                                  type='text'
                                  placeholder='1.0'
                                  {...field}
                                  onChange={field.onChange}
                                />
                              </FormControl>
                              <FormDescription>
                                {t('Multiplier for audio inputs.')}
                              </FormDescription>
                              <FormMessage />
                            </FormItem>
                          )}
                        />

                        <FormField
                          control={form.control}
                          name='audioCompletionRatio'
                          render={({ field }) => (
                            <FormItem>
                              <FormLabel>
                                {t('Audio completion ratio')}
                              </FormLabel>
                              <FormControl>
                                <Input
                                  type='text'
                                  placeholder='1.0'
                                  {...field}
                                  onChange={field.onChange}
                                />
                              </FormControl>
                              <FormDescription>
                                {t('Multiplier for audio outputs.')}
                              </FormDescription>
                              <FormMessage />
                            </FormItem>
                          )}
                        />
                      </CollapsibleContent>
                    </Collapsible>
                  </>
                )}
              </SideDrawerSection>
            ) : (
              <SideDrawerSection>
                <h3 className='text-sm font-semibold'>
                  {t('Pricing Configuration')}
                </h3>
                <p className='text-muted-foreground text-sm'>
                  {t(
                    'Only super administrators can rename models or change model pricing.'
                  )}
                </p>
              </SideDrawerSection>
            )}

            {/* Status & Sync */}
            <SideDrawerSection>
              <h3 className='text-sm font-semibold'>{t('Status & Sync')}</h3>

              <FormField
                control={form.control}
                name='status'
                render={({ field }) => (
                  <FormItem className={sideDrawerSwitchItemClassName()}>
                    <div className='flex flex-col gap-0.5'>
                      <FormLabel className='text-base'>
                        {t('Enabled')}
                      </FormLabel>
                      <FormDescription>
                        {t('Enable or disable this model')}
                      </FormDescription>
                    </div>
                    <FormControl>
                      <Switch
                        checked={field.value}
                        onCheckedChange={field.onChange}
                      />
                    </FormControl>
                  </FormItem>
                )}
              />

              <FormField
                control={form.control}
                name='sync_official'
                render={({ field }) => (
                  <FormItem className={sideDrawerSwitchItemClassName()}>
                    <div className='flex flex-col gap-0.5'>
                      <FormLabel className='text-base'>
                        {t('Official Sync')}
                      </FormLabel>
                      <FormDescription>
                        {t('Sync this model with official upstream')}
                      </FormDescription>
                    </div>
                    <FormControl>
                      <Switch
                        checked={field.value}
                        onCheckedChange={field.onChange}
                      />
                    </FormControl>
                  </FormItem>
                )}
              />
            </SideDrawerSection>
          </form>
        </Form>

        <SheetFooter className={sideDrawerFooterClassName()}>
          <SheetClose
            render={<Button variant='outline' disabled={isSubmitting} />}
          >
            {t('Cancel')}
          </SheetClose>
          <Button form='model-form' type='submit' disabled={isSubmitting}>
            {isSubmitting && <Loader2 className='mr-2 h-4 w-4 animate-spin' />}
            {isEditing ? t('Update Model') : t('Save changes')}
          </Button>
        </SheetFooter>
      </SheetContent>
    </Sheet>
  )
}
