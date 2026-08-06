/**
 * Copyright (c) 2026, WSO2 LLC. (https://www.wso2.com).
 *
 * WSO2 LLC. licenses this file to you under the Apache License,
 * Version 2.0 (the "License"); you may not use this file except
 * in compliance with the License.
 * You may obtain a copy of the License at
 *
 * http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing,
 * software distributed under the License is distributed on an
 * "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
 * KIND, either express or implied.  See the License for the
 * specific language governing permissions and limitations
 * under the License.
 */

import { Collapse } from "@wso2/oxygen-ui";

interface CollapsibleSectionProps {
    /** True once this section is known to have content worth showing. False
     * both while still loading and once resolved as not applicable — in
     * either case the section stays collapsed at zero height instead of
     * showing a placeholder sized differently from the real content. */
    show: boolean;
    children: React.ReactNode;
}

/**
 * Wraps a section whose content depends on an async fetch that may turn up
 * nothing. Renders collapsed (zero height, no skeleton) until `show` flips
 * true, then animates open — so a section that ends up empty never appears
 * at all, and one that does have content slides in instead of popping in at
 * a mismatched skeleton height.
 */
export const CollapsibleSection: React.FC<CollapsibleSectionProps> = ({ show, children }) => (
    <Collapse in={show} timeout={250}>
        {children}
    </Collapse>
);
