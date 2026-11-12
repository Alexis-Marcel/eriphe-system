import { createContext, useContext } from "react";
import { answerCard, proposal, type ProposalView } from "../data";

// Where the Proposal component reads a proposal and sends its answer:
// eriphe-core by default (data.ts); the sample and the tests give their own.
export type Proposals = {
  read(ref: string): Promise<ProposalView | null>;
  answer(card: number, yes: boolean): Promise<boolean>;
};

export const coreProposals: Proposals = { read: proposal, answer: answerCard };

export const ProposalsContext = createContext<Proposals>(coreProposals);
export const useProposals = () => useContext(ProposalsContext);

export type { ProposalView };
