import React, { useCallback, useState } from 'react'

import classNames from 'classnames'
import AlertCircleIcon from 'mdi-react/AlertCircleIcon'
import ChevronDownIcon from 'mdi-react/ChevronDownIcon'
import ChevronLeftIcon from 'mdi-react/ChevronLeftIcon'
import InformationOutlineIcon from 'mdi-react/InformationOutlineIcon'
import SearchIcon from 'mdi-react/SearchIcon'
import { Collapse, FormGroup, Input, Label } from 'reactstrap'

import { Form } from '@sourcegraph/branded/src/components/Form'
import { renderMarkdown } from '@sourcegraph/common'
import { SyntaxHighlightedSearchQuery } from '@sourcegraph/search-ui'
import { Markdown } from '@sourcegraph/shared/src/components/Markdown'
import { Skipped } from '@sourcegraph/shared/src/search/stream'
import { Button, Icon } from '@sourcegraph/wildcard'

import { StreamingProgressProps } from './StreamingProgress'

import styles from './StreamingProgressSkippedPopover.module.scss'

const severityToNumber = (severity: Skipped['severity']): number => {
    switch (severity) {
        case 'error':
            return 1
        case 'warn':
            return 2
        case 'info':
            return 3
    }
}

const sortBySeverity = (a: Skipped, b: Skipped): number => {
    const aSev = severityToNumber(a.severity)
    const bSev = severityToNumber(b.severity)

    return aSev - bSev
}

const SkippedMessage: React.FunctionComponent<{ skipped: Skipped; startOpen: boolean }> = ({ skipped, startOpen }) => {
    const [isOpen, setIsOpen] = useState(startOpen)

    const toggleIsOpen = useCallback(() => setIsOpen(oldValue => !oldValue), [])

    // Reactstrap is preventing default behavior on all non-DropdownItem elements inside a Dropdown,
    // so we need to stop propagation to allow normal behavior (e.g. enter and space to activate buttons)
    // See Reactstrap bug: https://github.com/reactstrap/reactstrap/issues/2099
    const onKeyDown = useCallback((event: React.KeyboardEvent<HTMLButtonElement>): void => {
        if (event.key === ' ' || event.key === 'Enter') {
            event.stopPropagation()
        }
    }, [])

    return (
        <div
            className={classNames(
                'pt-2 w-100',
                styles.streamingSkippedItem,
                skipped.severity !== 'info' && styles.streamingSkippedItemWarn
            )}
        >
            <Button
                className={classNames(styles.button, 'p-2 w-100 bg-transparent border-0')}
                onClick={toggleIsOpen}
                onKeyDown={onKeyDown}
                disabled={!skipped.message}
                outline={true}
                variant={skipped.severity !== 'info' ? 'danger' : 'primary'}
            >
                <h4 className="d-flex align-items-center mb-0 w-100">
                    <Icon
                        className={classNames(styles.icon, 'flex-shrink-0')}
                        as={skipped.severity === 'info' ? InformationOutlineIcon : AlertCircleIcon}
                    />

                    <span className="flex-grow-1 text-left">{skipped.title}</span>

                    {skipped.message && (
                        <Icon
                            className={classNames('flex-shrink-0', styles.chevron)}
                            as={isOpen ? ChevronDownIcon : ChevronLeftIcon}
                        />
                    )}
                </h4>
            </Button>

            {skipped.message && (
                <Collapse isOpen={isOpen}>
                    <Markdown
                        className={classNames(styles.message, styles.markdown, 'text-left py-1')}
                        dangerousInnerHTML={renderMarkdown(skipped.message)}
                    />
                </Collapse>
            )}
            <div className={classNames(styles.bottomBorderSpacer, 'mt-2')} />
        </div>
    )
}

export const StreamingProgressSkippedPopover: React.FunctionComponent<
    Pick<StreamingProgressProps, 'progress' | 'onSearchAgain'>
> = ({ progress, onSearchAgain }) => {
    const [selectedSuggestedSearches, setSelectedSuggestedSearches] = useState(new Set<string>())
    const submitHandler = useCallback(
        (event: React.FormEvent) => {
            onSearchAgain([...selectedSuggestedSearches])
            event.preventDefault()
        },
        [selectedSuggestedSearches, onSearchAgain]
    )
    const checkboxHandler = useCallback((event: React.FormEvent<HTMLInputElement>) => {
        const itemToToggle = event.currentTarget.value
        const checked = event.currentTarget.checked
        setSelectedSuggestedSearches(selected => {
            const newSelected = new Set(selected)
            if (checked) {
                newSelected.add(itemToToggle)
            } else {
                newSelected.delete(itemToToggle)
            }
            return newSelected
        })
    }, [])

    const sortedSkippedItems = progress.skipped.sort(sortBySeverity)

    return (
        <>
            {sortedSkippedItems.map((skipped, index) => (
                <SkippedMessage
                    key={skipped.reason}
                    skipped={skipped}
                    // Start with first item open, but only if it's not info severity or if there's only one item
                    startOpen={index === 0 && (skipped.severity !== 'info' || sortedSkippedItems.length === 1)}
                />
            ))}
            {sortedSkippedItems.some(skipped => skipped.suggested) && (
                <Form className="pb-3 px-3" onSubmit={submitHandler} data-testid="popover-form">
                    <div className="mb-2 mt-3">Search again:</div>
                    <FormGroup check={true}>
                        {sortedSkippedItems.map(
                            skipped =>
                                skipped.suggested && (
                                    <Label
                                        check={true}
                                        className="mb-1 d-block"
                                        key={skipped.suggested.queryExpression}
                                    >
                                        <Input
                                            type="checkbox"
                                            value={skipped.suggested.queryExpression}
                                            onChange={checkboxHandler}
                                            data-testid="streaming-progress-skipped-suggest-check"
                                        />{' '}
                                        {skipped.suggested.title} (
                                        <SyntaxHighlightedSearchQuery query={skipped.suggested.queryExpression} />)
                                    </Label>
                                )
                        )}
                    </FormGroup>

                    <Button
                        type="submit"
                        className="mt-2"
                        variant="primary"
                        disabled={selectedSuggestedSearches.size === 0}
                        data-testid="skipped-popover-form-submit-btn"
                    >
                        <Icon className="mr-1" as={SearchIcon} />
                        Search again
                    </Button>
                </Form>
            )}
        </>
    )
}
