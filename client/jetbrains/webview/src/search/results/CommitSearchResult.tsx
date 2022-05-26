import React from 'react'

import classNames from 'classnames'

import { CodeHostIcon, formatRepositoryStarCount, SearchResultStar } from '@sourcegraph/search-ui'
import { displayRepoName } from '@sourcegraph/shared/src/components/RepoLink'
import { CommitMatch } from '@sourcegraph/shared/src/search/stream'
// eslint-disable-next-line no-restricted-imports
import { Timestamp } from '@sourcegraph/web/src/components/time/Timestamp'
import { Typography, useIsTruncated } from '@sourcegraph/wildcard'

import { getResultIdForCommitMatch } from './utils'

import styles from './CommitSearchResult.module.scss'

interface Props {
    selectResult: (id: string) => void
    selectedResult: null | string
    match: CommitMatch
}

export const CommitSearchResult: React.FunctionComponent<Props> = ({ match, selectedResult, selectResult }: Props) => {
    const [titleReference, truncated, checkTruncation] = useIsTruncated()

    const formattedRepositoryStarCount = formatRepositoryStarCount(match.repoStars)

    const resultId = getResultIdForCommitMatch(match)
    const onClick = (): void => selectResult(resultId)

    return (
        // The below element's accessibility is handled via a document level event listener.
        //
        // eslint-disable-next-line jsx-a11y/click-events-have-key-events,jsx-a11y/no-static-element-interactions
        <div
            id={`search-result-list-item-${resultId}`}
            className={classNames(styles.line, {
                [styles.lineActive]: resultId === selectedResult,
            })}
            onMouseDown={preventAll}
            onClick={onClick}
            key={resultId}
        >
            <CodeHostIcon repoName={match.repository} className="text-muted flex-shrink-0" />
            <span
                onMouseEnter={checkTruncation}
                ref={titleReference}
                data-tooltip={(truncated && `${match.authorName}: ${match.message.split('\n', 1)[0]}`) || null}
            >{`${displayRepoName(match.repository)} › ${match.authorName}: ${match.message.split('\n', 1)[0]}`}</span>
            <span className={styles.spacer} />
            <Typography.Code className={styles.commitOid}>{match.oid.slice(0, 7)}</Typography.Code>{' '}
            <Timestamp date={match.authorDate} noAbout={true} strict={true} />
            {formattedRepositoryStarCount && (
                <>
                    <div className={styles.divider} />
                    <SearchResultStar />
                    {formattedRepositoryStarCount}
                </>
            )}
        </div>
    )
}

function preventAll(event: React.MouseEvent): void {
    event.stopPropagation()
    event.preventDefault()
}
