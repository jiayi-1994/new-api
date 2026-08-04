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
import { after, describe, test } from 'node:test'

import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { createInstance } from 'i18next'
import type React from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { I18nextProvider } from 'react-i18next'

// @ts-expect-error Bun exposes module mocks at runtime without installed types.
const { mock } = await import('bun:test')

function Container(props: React.PropsWithChildren) {
  return <div>{props.children}</div>
}

mock.module('@tanstack/react-router', () => ({
  Link: Container,
  Outlet: Container,
  redirect: (options: unknown) => options,
  useBlocker: () => ({ proceed: () => undefined, reset: () => undefined }),
  useLocation: () => ({ pathname: '/' }),
  useNavigate: () => () => undefined,
  useParams: () => ({}),
  useRouterState: () => ({ location: { pathname: '/' } }),
}))
mock.module('@/components/ui/command', () => ({
  Command: Container,
  CommandDialog: Container,
  CommandEmpty: Container,
  CommandGroup: Container,
  CommandInput: Container,
  CommandItem: Container,
  CommandList: Container,
  CommandSeparator: Container,
}))
mock.module('@/context/search-provider', () => ({
  useSearch: () => ({ open: true, setOpen: () => undefined }),
}))
mock.module('@/context/theme-provider', () => ({
  useTheme: () => ({ setTheme: () => undefined }),
}))
mock.module('@/components/ui/scroll-area', () => ({
  ScrollArea: Container,
}))

const { CommandMenu } = await import('../command-menu')

async function renderCommandMenu(playgroundEnabled: boolean): Promise<string> {
  const i18n = createInstance()
  await i18n.init({
    lng: 'en',
    resources: {
      en: { translation: { 'Infinite Canvas': 'Infinite Canvas' } },
    },
  })

  const queryClient = new QueryClient()
  queryClient.setQueryData(['status'], {
    SidebarModulesAdmin: JSON.stringify({
      chat: { enabled: true, playground: playgroundEnabled, chat: true },
    }),
  })

  const markup = renderToStaticMarkup(
    <QueryClientProvider client={queryClient}>
      <I18nextProvider i18n={i18n}>
        <CommandMenu />
      </I18nextProvider>
    </QueryClientProvider>
  )
  queryClient.clear()
  return markup
}

describe('command menu navigation', () => {
  after(() => mock.restore())

  test('hides Infinite Canvas when Playground is disabled', async () => {
    const markup = await renderCommandMenu(false)

    assert.equal(markup.includes('Infinite Canvas'), false)
  })
})
