-- Forum-shaped schema for the MariaDB integration harness (issue #115).
--
-- Tables are cribbed from the production XenForo database (MariaDB 11.5)
-- and cover exactly what the API reads: the NF Rosters add-on tables, the
-- NF Tickets add-on tables, the Cav7 ApiKeyManager tables, and the core
-- xf_user / xf_user_connected_account / xf_post / xf_phrase tables.
--
-- Index fidelity matters here: the harness carries production's stock
-- indexes and must NOT carry the four indexes proposed by PRD #112
-- (xf_post user_id_post_date; idx_relation_id on
-- xf_nf_rosters_service_record and xf_nf_rosters_user_award;
-- idx_user_id on xf_nf_rosters_user). Their absence is what makes the
-- "red" EXPLAIN plans of the index slice (#119) reproducible. Do not add
-- them to this file.

-- ---------------------------------------------------------------------
-- Core XenForo
-- ---------------------------------------------------------------------

CREATE TABLE `xf_user` (
  `user_id` int(10) unsigned NOT NULL AUTO_INCREMENT,
  `username` varchar(50) NOT NULL,
  `username_date` int(10) unsigned NOT NULL DEFAULT 0,
  `username_date_visible` int(10) unsigned NOT NULL DEFAULT 0,
  `email` varchar(120) NOT NULL,
  `custom_title` varchar(50) NOT NULL DEFAULT '',
  `language_id` int(10) unsigned NOT NULL,
  `style_id` int(10) unsigned NOT NULL COMMENT '0 = use system default',
  `style_variation` varchar(50) NOT NULL DEFAULT '',
  `timezone` varchar(50) NOT NULL COMMENT 'Example: ''Europe/London''',
  `visible` tinyint(3) unsigned NOT NULL DEFAULT 1 COMMENT 'Show browsing activity to others',
  `activity_visible` tinyint(3) unsigned NOT NULL DEFAULT 1,
  `user_group_id` int(10) unsigned NOT NULL,
  `secondary_group_ids` varbinary(255) NOT NULL,
  `display_style_group_id` int(10) unsigned NOT NULL DEFAULT 0 COMMENT 'User group ID that provides user styling',
  `permission_combination_id` int(10) unsigned NOT NULL,
  `message_count` int(10) unsigned NOT NULL DEFAULT 0,
  `question_solution_count` int(10) unsigned NOT NULL DEFAULT 0,
  `conversations_unread` smallint(5) unsigned NOT NULL DEFAULT 0,
  `register_date` int(10) unsigned NOT NULL DEFAULT 0,
  `last_activity` int(10) unsigned NOT NULL DEFAULT 0,
  `last_summary_email_date` int(10) unsigned DEFAULT NULL,
  `trophy_points` int(10) unsigned NOT NULL DEFAULT 0,
  `alerts_unviewed` smallint(5) unsigned NOT NULL DEFAULT 0,
  `alerts_unread` smallint(5) unsigned NOT NULL DEFAULT 0,
  `avatar_date` int(10) unsigned NOT NULL DEFAULT 0,
  `avatar_width` smallint(5) unsigned NOT NULL DEFAULT 0,
  `avatar_height` smallint(5) unsigned NOT NULL DEFAULT 0,
  `avatar_highdpi` tinyint(3) unsigned NOT NULL DEFAULT 0,
  `avatar_optimized` tinyint(3) unsigned NOT NULL DEFAULT 0,
  `gravatar` varchar(120) NOT NULL DEFAULT '',
  `user_state` enum('valid','email_confirm','email_confirm_edit','moderated','email_bounce','rejected','disabled') NOT NULL DEFAULT 'valid',
  `security_lock` enum('','change','reset') NOT NULL DEFAULT '',
  `is_moderator` tinyint(3) unsigned NOT NULL DEFAULT 0,
  `is_admin` tinyint(3) unsigned NOT NULL DEFAULT 0,
  `is_banned` tinyint(3) unsigned NOT NULL DEFAULT 0,
  `reaction_score` int(11) NOT NULL DEFAULT 0,
  `vote_score` int(11) NOT NULL DEFAULT 0,
  `warning_points` int(10) unsigned NOT NULL DEFAULT 0,
  `is_staff` tinyint(3) unsigned NOT NULL DEFAULT 0,
  `secret_key` varbinary(32) NOT NULL,
  `privacy_policy_accepted` int(10) unsigned NOT NULL DEFAULT 0,
  `terms_accepted` int(10) unsigned NOT NULL DEFAULT 0,
  `nf_calendar_event_count` int(10) unsigned NOT NULL DEFAULT 0,
  `teamspeak_identity_count` int(10) unsigned NOT NULL DEFAULT 0,
  `dbtech_donate_total` double(10,2) NOT NULL DEFAULT 0.00,
  `dbtech_donate_public` double(10,2) NOT NULL DEFAULT 0.00,
  `dbtech_donate_anonymous` double(10,2) NOT NULL DEFAULT 0.00,
  `nf_tickets_count` int(10) unsigned NOT NULL DEFAULT 0,
  `nf_tickets_unread` int(10) unsigned NOT NULL DEFAULT 0,
  `snog_forms` blob DEFAULT NULL,
  `siropu_donation_count` int(10) unsigned NOT NULL DEFAULT 1,
  `siropu_donation_amount` decimal(10,2) NOT NULL DEFAULT 0.00,
  `siropu_donation_date` int(10) unsigned NOT NULL DEFAULT 0,
  PRIMARY KEY (`user_id`),
  UNIQUE KEY `username` (`username`),
  KEY `email` (`email`),
  KEY `permission_combination_id` (`permission_combination_id`),
  KEY `user_state` (`user_state`),
  KEY `last_activity` (`last_activity`),
  KEY `last_summary_email_date` (`last_summary_email_date`),
  KEY `message_count` (`message_count`),
  KEY `trophy_points` (`trophy_points`),
  KEY `reaction_score` (`reaction_score`),
  KEY `register_date` (`register_date`),
  KEY `question_solution_count` (`question_solution_count`),
  KEY `vote_score` (`vote_score`),
  KEY `staff_username` (`is_staff`,`username`),
  KEY `nf_calendar_event_count` (`nf_calendar_event_count`),
  KEY `teamspeak_identity_count` (`teamspeak_identity_count`),
  KEY `nf_tickets_count` (`nf_tickets_count`),
  KEY `nf_tickets_unread` (`nf_tickets_unread`),
  KEY `siropu_donation_count` (`siropu_donation_count`),
  KEY `siropu_donation_amount` (`siropu_donation_amount`),
  KEY `siropu_donation_date` (`siropu_donation_date`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;

-- NOTE: stock XenForo indexes only. Production carries no
-- (user_id, post_date) composite — PRD #112 proposes one
-- (user_id_post_date) to unlock the loose index scan on the hot
-- last-post aggregation. Do not add it here.
CREATE TABLE `xf_post` (
  `post_id` int(10) unsigned NOT NULL AUTO_INCREMENT,
  `thread_id` int(10) unsigned NOT NULL,
  `user_id` int(10) unsigned NOT NULL,
  `username` varchar(50) NOT NULL,
  `post_date` int(10) unsigned NOT NULL,
  `message` mediumtext NOT NULL,
  `ip_id` int(10) unsigned NOT NULL DEFAULT 0,
  `message_state` enum('visible','moderated','deleted') NOT NULL DEFAULT 'visible',
  `attach_count` smallint(5) unsigned NOT NULL DEFAULT 0,
  `position` int(10) unsigned NOT NULL,
  `type_data` mediumblob NOT NULL,
  `reaction_score` int(11) NOT NULL DEFAULT 0,
  `reactions` blob DEFAULT NULL,
  `reaction_users` blob NOT NULL,
  `vote_score` int(11) NOT NULL,
  `vote_count` int(10) unsigned NOT NULL DEFAULT 0,
  `warning_id` int(10) unsigned NOT NULL DEFAULT 0,
  `warning_message` varchar(255) NOT NULL DEFAULT '',
  `last_edit_date` int(10) unsigned NOT NULL DEFAULT 0,
  `last_edit_user_id` int(10) unsigned NOT NULL DEFAULT 0,
  `edit_count` int(10) unsigned NOT NULL DEFAULT 0,
  `embed_metadata` blob DEFAULT NULL,
  PRIMARY KEY (`post_id`),
  KEY `thread_id_post_date` (`thread_id`,`post_date`),
  KEY `thread_id_position` (`thread_id`,`position`),
  KEY `thread_id_score_date` (`thread_id`,`vote_score`,`post_date`),
  KEY `user_id` (`user_id`),
  KEY `post_date` (`post_date`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;

CREATE TABLE `xf_user_connected_account` (
  `user_id` int(10) unsigned NOT NULL,
  `provider` varbinary(25) NOT NULL,
  `provider_key` varbinary(150) NOT NULL,
  `extra_data` mediumblob NOT NULL,
  PRIMARY KEY (`user_id`,`provider`),
  UNIQUE KEY `provider` (`provider`,`provider_key`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;

-- ---------------------------------------------------------------------
-- NF Rosters add-on (milpacs)
-- ---------------------------------------------------------------------

-- NOTE: production carries no index on user_id (PRD #112 proposes
-- idx_user_id). Do not add one here.
CREATE TABLE `xf_nf_rosters_user` (
  `relation_id` int(10) unsigned NOT NULL AUTO_INCREMENT,
  `roster_id` int(10) unsigned NOT NULL,
  `user_id` int(10) unsigned NOT NULL,
  `username` text NOT NULL,
  `position_id` int(10) unsigned NOT NULL,
  `secondary_position_ids` blob NOT NULL,
  `rank_id` int(10) unsigned NOT NULL,
  `bio` text NOT NULL,
  `uniform_date` int(10) unsigned NOT NULL DEFAULT 0,
  `added_date` int(10) unsigned NOT NULL DEFAULT 0,
  `custom_fields` mediumblob NOT NULL,
  PRIMARY KEY (`relation_id`),
  KEY `idx_roster_id` (`roster_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;

CREATE TABLE `xf_nf_rosters_rank` (
  `rank_id` int(10) unsigned NOT NULL AUTO_INCREMENT,
  `title` varchar(50) NOT NULL,
  `rank_image` int(10) unsigned NOT NULL DEFAULT 0,
  `display_order` int(10) unsigned NOT NULL DEFAULT 0,
  `extra_group_ids` blob NOT NULL,
  PRIMARY KEY (`rank_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;

CREATE TABLE `xf_nf_rosters_position` (
  `position_id` int(10) unsigned NOT NULL AUTO_INCREMENT,
  `position_title` varchar(50) NOT NULL,
  `position_group_id` int(10) unsigned NOT NULL DEFAULT 0,
  `display_order` int(10) unsigned NOT NULL DEFAULT 0,
  `materialized_order` int(10) unsigned NOT NULL DEFAULT 0,
  `extra_group_ids` blob NOT NULL,
  `possible_secondary` tinyint(3) unsigned NOT NULL DEFAULT 1,
  PRIMARY KEY (`position_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;

CREATE TABLE `xf_nf_rosters_position_group` (
  `position_group_id` int(10) unsigned NOT NULL AUTO_INCREMENT,
  `title` varchar(50) NOT NULL,
  `display_order` int(10) unsigned NOT NULL DEFAULT 0,
  PRIMARY KEY (`position_group_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;

CREATE TABLE `xf_nf_rosters_award` (
  `award_id` int(10) unsigned NOT NULL AUTO_INCREMENT,
  `title` varchar(150) NOT NULL,
  `award_image` int(10) unsigned NOT NULL DEFAULT 0,
  `award_group_id` int(10) unsigned NOT NULL DEFAULT 0,
  `display_order` int(10) unsigned NOT NULL DEFAULT 0,
  `materialized_order` int(10) unsigned NOT NULL DEFAULT 0,
  PRIMARY KEY (`award_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;

-- NOTE: production carries no secondary index (PRD #112 proposes
-- idx_relation_id). Do not add one here.
CREATE TABLE `xf_nf_rosters_user_award` (
  `record_id` int(11) NOT NULL AUTO_INCREMENT,
  `relation_id` int(11) NOT NULL,
  `award_id` int(11) NOT NULL,
  `from_user_id` int(11) NOT NULL,
  `details` text NOT NULL,
  `award_date` int(11) NOT NULL DEFAULT 0,
  `citation_date` int(11) NOT NULL DEFAULT 0,
  PRIMARY KEY (`record_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;

-- NOTE: production carries no secondary index (PRD #112 proposes
-- idx_relation_id). Do not add one here.
CREATE TABLE `xf_nf_rosters_service_record` (
  `record_id` int(10) unsigned NOT NULL AUTO_INCREMENT,
  `relation_id` int(10) unsigned NOT NULL,
  `details` text DEFAULT NULL,
  `record_date` int(10) unsigned NOT NULL DEFAULT 0,
  `citation_date` int(10) unsigned NOT NULL DEFAULT 0,
  `record_type_id` int(10) unsigned NOT NULL DEFAULT 0,
  PRIMARY KEY (`record_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;

CREATE TABLE `xf_nf_rosters_field_value` (
  `relation_id` int(10) unsigned NOT NULL,
  `field_id` varbinary(25) NOT NULL,
  `field_value` mediumtext NOT NULL,
  PRIMARY KEY (`relation_id`,`field_id`),
  KEY `field_id` (`field_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;
