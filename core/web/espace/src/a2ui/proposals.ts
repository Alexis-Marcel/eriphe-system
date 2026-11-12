import { createContext, useContext } from "react";
import { answerProposal, proposal, type Answer, type ProposalView } from "../data";

// Where the Proposal component reads a proposal and sends its answer:
// eriphe-core by default (data.ts: an "answer" action on the surface); the
// sample and the tests give their own.
export type Proposals = {
  read(ref: string): Promise<ProposalView | null>;
  answer(a: Answer): Promise<boolean>;
};

export const coreProposals: Proposals = { read: proposal, answer: answerProposal };

export const ProposalsContext = createContext<Proposals>(coreProposals);
export const useProposals = () => useContext(ProposalsContext);

export type { Answer, ProposalView };
