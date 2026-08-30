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
import assert from 'node:assert/strict'
import { describe, test } from 'node:test'

import {
  applyModelPricingMutation,
  buildModelPricingSelection,
  buildPricingDocumentReplacement,
  type PricingDocuments,
} from '../model-pricing-persistence'

function documentsFixture(): PricingDocuments {
  return {
    ModelPrice: { source: 0.3, target: 9 },
    ModelRatio: { source: 1.5, target: 8 },
    CacheRatio: { source: 0.4, target: 7 },
    CreateCacheRatio: { source: 1.25, target: 6 },
    CompletionRatio: { source: 2, target: 5 },
    ImageRatio: { source: 3, target: 4 },
    AudioRatio: { source: 4, target: 3 },
    AudioCompletionRatio: { source: 5, target: 2 },
    'billing_setting.billing_mode': {
      source: 'tiered_expr',
      target: 'tiered_expr',
    },
    'billing_setting.billing_expr': {
      source: 'v1:tier("base", p * 1)',
      target: 'target-expression',
    },
    TaskBillingMode: { source: 'per_call', target: 'per_second' },
    VideoResolutionPrice: { target: { '4k': 9 } },
  }
}

describe('model pricing persistence', () => {
  test('resolution save retains the complete legacy snapshot', () => {
    const result = applyModelPricingMutation(documentsFixture(), {
      kind: 'save',
      name: 'source',
      selection: {
        mode: 'video_resolution',
        resolution_prices: { '720p': 0.1 },
      },
    })

    assert.equal(result.ModelPrice.source, 0.3)
    assert.equal(result.ModelRatio.source, 1.5)
    assert.equal(result.CacheRatio.source, 0.4)
    assert.equal(result.CreateCacheRatio.source, 1.25)
    assert.equal(result.CompletionRatio.source, 2)
    assert.equal(result.ImageRatio.source, 3)
    assert.equal(result.AudioRatio.source, 4)
    assert.equal(result.AudioCompletionRatio.source, 5)
    assert.equal(result['billing_setting.billing_mode'].source, 'tiered_expr')
    assert.equal(
      result['billing_setting.billing_expr'].source,
      'v1:tier("base", p * 1)'
    )
    assert.equal(result.TaskBillingMode.source, 'per_call')
    assert.deepEqual(result.VideoResolutionPrice.source, { '720p': 0.1 })
  })

  test('explicit legacy fixed save removes the table and inactive legacy fields', () => {
    const documents = applyModelPricingMutation(documentsFixture(), {
      kind: 'save',
      name: 'source',
      selection: {
        mode: 'video_resolution',
        resolution_prices: { '720p': 0.1 },
      },
    })
    const result = applyModelPricingMutation(documents, {
      kind: 'save',
      name: 'source',
      selection: {
        mode: 'per_request',
        price: 0.6,
        task_billing_mode: 'per_call',
      },
    })

    assert.equal(result.ModelPrice.source, 0.6)
    assert.equal(result.TaskBillingMode.source, 'per_call')
    assert.equal(result.ModelRatio.source, undefined)
    assert.equal(result['billing_setting.billing_expr'].source, undefined)
    assert.equal(result.VideoResolutionPrice.source, undefined)
  })

  test('ratio and expression saves enforce their established mutual exclusion', () => {
    const ratioResult = applyModelPricingMutation(documentsFixture(), {
      kind: 'save',
      name: 'source',
      selection: {
        mode: 'per_token',
        ratio: 1.2,
        cache_ratio: 0.3,
        create_cache_ratio: 1.1,
      },
    })

    assert.equal(ratioResult.ModelPrice.source, undefined)
    assert.equal(ratioResult.ModelRatio.source, 1.2)
    assert.equal(ratioResult.CacheRatio.source, 0.3)
    assert.equal(ratioResult.CreateCacheRatio.source, 1.1)
    assert.equal(ratioResult.VideoResolutionPrice.source, undefined)
    assert.equal(ratioResult['billing_setting.billing_mode'].source, undefined)

    const expressionResult = applyModelPricingMutation(documentsFixture(), {
      kind: 'save',
      name: 'source',
      selection: {
        mode: 'tiered_expr',
        billing_expr: 'v1:tier("base", p * 2)',
        price: 0.7,
        ratio: 1.4,
        create_cache_ratio: 1.3,
      },
    })

    assert.equal(expressionResult.ModelPrice.source, 0.7)
    assert.equal(expressionResult.ModelRatio.source, 1.4)
    assert.equal(expressionResult.CreateCacheRatio.source, 1.3)
    assert.equal(
      expressionResult['billing_setting.billing_mode'].source,
      'tiered_expr'
    )
    assert.equal(
      expressionResult['billing_setting.billing_expr'].source,
      'v1:tier("base", p * 2)'
    )
    assert.equal(expressionResult.VideoResolutionPrice.source, undefined)
  })

  test('copy replaces every target document and leaves the source intact', () => {
    const documents = documentsFixture()
    documents.VideoResolutionPrice.source = { '720p': 0.1 }

    const result = applyModelPricingMutation(documents, {
      kind: 'copy',
      sourceName: 'source',
      targetName: 'target',
    })

    assert.equal(result.ModelPrice.source, 0.3)
    assert.equal(result.ModelPrice.target, 0.3)
    assert.equal(result.CreateCacheRatio.target, 1.25)
    assert.equal(result.TaskBillingMode.target, 'per_call')
    assert.deepEqual(result.VideoResolutionPrice.source, { '720p': 0.1 })
    assert.deepEqual(result.VideoResolutionPrice.target, { '720p': 0.1 })
  })

  test('copy clears target entries missing from the source', () => {
    const documents = documentsFixture()
    delete documents.CreateCacheRatio.source
    delete documents.VideoResolutionPrice.source

    const result = applyModelPricingMutation(documents, {
      kind: 'copy',
      sourceName: 'source',
      targetName: 'target',
    })

    assert.equal(result.CreateCacheRatio.target, undefined)
    assert.equal(result.VideoResolutionPrice.target, undefined)
  })

  test('rename moves source entries and preserves target entries absent at source', () => {
    const documents = documentsFixture()
    delete documents.CreateCacheRatio.source
    documents.VideoResolutionPrice.source = { '720p': 0.1 }

    const result = applyModelPricingMutation(documents, {
      kind: 'rename',
      sourceName: 'source',
      targetName: 'target',
    })

    assert.equal(result.ModelPrice.source, undefined)
    assert.equal(result.ModelPrice.target, 0.3)
    assert.equal(result.CreateCacheRatio.target, 6)
    assert.deepEqual(result.VideoResolutionPrice.target, { '720p': 0.1 })
  })

  test('delete removes the model from all twelve documents', () => {
    const documents = documentsFixture()
    documents.VideoResolutionPrice.source = { '720p': 0.1 }

    const result = applyModelPricingMutation(documents, {
      kind: 'delete',
      name: 'source',
    })

    for (const document of Object.values(result)) {
      assert.equal(document.source, undefined)
    }
  })

  test('selection builder carries CreateCacheRatio and retained resolution legacy fields', () => {
    const selection = buildModelPricingSelection({
      name: 'source',
      billingMode: 'video_resolution',
      price: '0.3',
      ratio: '1.5',
      cacheRatio: '0.4',
      createCacheRatio: '1.25',
      completionRatio: '2',
      imageRatio: '3',
      audioRatio: '4',
      audioCompletionRatio: '5',
      billingExpr: 'tier("base", p)',
      requestRuleExpr: 'if(r.size > 1, 2, 1)',
      taskBillingMode: 'per_call',
      resolutionPrices: { '720p': 0.1 },
    })

    assert.deepEqual(selection, {
      mode: 'video_resolution',
      price: 0.3,
      ratio: 1.5,
      cache_ratio: 0.4,
      create_cache_ratio: 1.25,
      completion_ratio: 2,
      image_ratio: 3,
      audio_ratio: 4,
      audio_completion_ratio: 5,
      billing_expr: '(tier("base", p)) * if(r.size > 1, 2, 1)',
      task_billing_mode: 'per_call',
      resolution_prices: { '720p': 0.1 },
    })
  })

  test('replacement command compares normalized values but keeps exact raw CAS bytes', () => {
    const replacement = buildPricingDocumentReplacement(
      { ModelPrice: '{"video":0.2}', ModelRatio: '{}' },
      { ModelPrice: '{\n  "video": 0.3\n}', ModelRatio: '{}' },
      { ModelPrice: '{ "video": 0.2 }', ModelRatio: '{ }' }
    )

    assert.deepEqual(replacement, {
      values: { ModelPrice: '{"video":0.3}' },
      expected_documents: { ModelPrice: '{ "video": 0.2 }' },
    })
  })

  for (const invalidPrice of ['', '.', '1.', '-1', '1e2']) {
    test(`refuses to build an incomplete fixed price ${JSON.stringify(invalidPrice)}`, () => {
      const original = documentsFixture()

      assert.throws(() =>
        buildModelPricingSelection({
          name: 'video',
          billingMode: 'per-request',
          price: invalidPrice,
        })
      )
      assert.equal(original.ModelPrice.source, 0.3)
      assert.equal(original.ModelRatio.source, 1.5)
    })
  }

  for (const invalidRatio of ['', '.', '1.', '-1', '1e2']) {
    test(`refuses to build an incomplete model ratio ${JSON.stringify(invalidRatio)}`, () => {
      assert.throws(() =>
        buildModelPricingSelection({
          name: 'video',
          billingMode: 'per-token',
          ratio: invalidRatio,
        })
      )
    })
  }

  test('refuses an invalid populated optional ratio instead of omitting it', () => {
    assert.throws(() =>
      buildModelPricingSelection({
        name: 'video',
        billingMode: 'per-token',
        ratio: '1',
        createCacheRatio: '1.',
      })
    )
  })

  test('rejects a semantically incomplete ratio selection before deleting existing documents', () => {
    const original = documentsFixture()

    assert.throws(() =>
      applyModelPricingMutation(original, {
        kind: 'save',
        name: 'source',
        selection: { mode: 'per_token' },
      })
    )
    assert.equal(original.ModelPrice.source, 0.3)
    assert.equal(original.ModelRatio.source, 1.5)
  })

  test('rejects a negative numeric selection before deleting existing documents', () => {
    const original = documentsFixture()

    assert.throws(() =>
      applyModelPricingMutation(original, {
        kind: 'save',
        name: 'source',
        selection: { mode: 'per_token', ratio: -1 },
      })
    )
    assert.equal(original.ModelPrice.source, 0.3)
    assert.equal(original.ModelRatio.source, 1.5)
  })
})
